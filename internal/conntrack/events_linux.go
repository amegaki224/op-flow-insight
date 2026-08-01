//go:build linux

package conntrack

import (
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"sync"
	"syscall"
)

const (
	netlinkNetfilter        = 12
	nfnlSubsysCTNetlink     = 1
	ipctnlMsgCTDelete       = 2
	nfnlgrpConntrackDestroy = 3

	ctaTupleOrig     = 1
	ctaTupleReply    = 2
	ctaCountersOrig  = 9
	ctaCountersReply = 10
	ctaZone          = 18

	ctaTupleIP    = 1
	ctaTupleProto = 2

	ctaIPv4Src = 1
	ctaIPv4Dst = 2
	ctaIPv6Src = 3
	ctaIPv6Dst = 4

	ctaProtoNum     = 1
	ctaProtoSrcPort = 2
	ctaProtoDstPort = 3

	ctaCountersBytes = 2

	nlaTypeMask = 0x3fff
)

func listenDestroy(ctx context.Context, handler DestroyHandler) error {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW, netlinkNetfilter)
	if err != nil {
		return err
	}
	var closeOnce sync.Once
	closeFD := func() {
		closeOnce.Do(func() { _ = syscall.Close(fd) })
	}
	defer closeFD()
	_ = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_RCVBUF, 4<<20)
	addr := &syscall.SockaddrNetlink{
		Family: syscall.AF_NETLINK,
		Groups: 1 << (nfnlgrpConntrackDestroy - 1),
	}
	if err := syscall.Bind(fd, addr); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			closeFD()
		case <-done:
		}
	}()
	defer close(done)

	buffer := make([]byte, 1<<20)
	for {
		n, _, err := syscall.Recvfrom(fd, buffer, 0)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, syscall.EBADF) {
				return nil
			}
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			return err
		}
		parseNetlinkBatch(buffer[:n], handler)
	}
}

func parseNetlinkBatch(raw []byte, handler DestroyHandler) {
	for len(raw) >= 16 {
		length := int(binary.LittleEndian.Uint32(raw[0:4]))
		if length < 16 || length > len(raw) {
			return
		}
		msgType := binary.LittleEndian.Uint16(raw[4:6])
		expected := uint16((nfnlSubsysCTNetlink << 8) | ipctnlMsgCTDelete)
		if msgType == expected {
			if conn, ok := parseDestroyMessage(raw[16:length]); ok {
				handler(conn)
			}
		}
		aligned := (length + 3) &^ 3
		if aligned > len(raw) {
			return
		}
		raw = raw[aligned:]
	}
}

func parseDestroyMessage(payload []byte) (Connection, bool) {
	// struct nfgenmsg: family, version, resource id.
	if len(payload) < 4 {
		return Connection{}, false
	}
	family := payload[0]
	attrs := parseAttrs(payload[4:])
	origRaw, ok1 := firstAttr(attrs, ctaTupleOrig)
	replyRaw, ok2 := firstAttr(attrs, ctaTupleReply)
	origCounters, ok3 := firstAttr(attrs, ctaCountersOrig)
	replyCounters, ok4 := firstAttr(attrs, ctaCountersReply)
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return Connection{}, false
	}
	orig, proto, ok1 := parseTuple(origRaw, family)
	reply, _, ok2 := parseTuple(replyRaw, family)
	orig.Bytes, ok3 = parseCounterBytes(origCounters)
	reply.Bytes, ok4 = parseCounterBytes(replyCounters)
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return Connection{}, false
	}
	conn := Connection{
		Family: familyName(family), Protocol: protocolName(proto),
		Original: orig, Reply: reply, HasBytes: true,
	}
	if zone, found := firstAttr(attrs, ctaZone); found && len(zone) >= 2 {
		conn.Zone = binary.BigEndian.Uint16(zone[:2])
	}
	return conn, true
}

func parseTuple(raw []byte, family byte) (Tuple, byte, bool) {
	attrs := parseAttrs(raw)
	ipRaw, ok1 := firstAttr(attrs, ctaTupleIP)
	protoRaw, ok2 := firstAttr(attrs, ctaTupleProto)
	if !ok1 || !ok2 {
		return Tuple{}, 0, false
	}
	ipAttrs := parseAttrs(ipRaw)
	protoAttrs := parseAttrs(protoRaw)
	var tuple Tuple
	var sourceType, destType uint16
	if family == syscall.AF_INET {
		sourceType, destType = ctaIPv4Src, ctaIPv4Dst
	} else if family == syscall.AF_INET6 {
		sourceType, destType = ctaIPv6Src, ctaIPv6Dst
	} else {
		return Tuple{}, 0, false
	}
	src, ok1 := firstAttr(ipAttrs, sourceType)
	dst, ok2 := firstAttr(ipAttrs, destType)
	proto, ok3 := firstAttr(protoAttrs, ctaProtoNum)
	if !ok1 || !ok2 || !ok3 || len(proto) < 1 {
		return Tuple{}, 0, false
	}
	if family == syscall.AF_INET && len(src) >= 4 && len(dst) >= 4 {
		tuple.Source = netip.AddrFrom4([4]byte(src[:4]))
		tuple.Destination = netip.AddrFrom4([4]byte(dst[:4]))
	} else if family == syscall.AF_INET6 && len(src) >= 16 && len(dst) >= 16 {
		tuple.Source = netip.AddrFrom16([16]byte(src[:16]))
		tuple.Destination = netip.AddrFrom16([16]byte(dst[:16]))
	} else {
		return Tuple{}, 0, false
	}
	if port, ok := firstAttr(protoAttrs, ctaProtoSrcPort); ok && len(port) >= 2 {
		tuple.SourcePort = binary.BigEndian.Uint16(port[:2])
	}
	if port, ok := firstAttr(protoAttrs, ctaProtoDstPort); ok && len(port) >= 2 {
		tuple.DestPort = binary.BigEndian.Uint16(port[:2])
	}
	return tuple, proto[0], true
}

func parseCounterBytes(raw []byte) (uint64, bool) {
	value, ok := firstAttr(parseAttrs(raw), ctaCountersBytes)
	if !ok {
		return 0, false
	}
	switch {
	case len(value) >= 8:
		return binary.BigEndian.Uint64(value[:8]), true
	case len(value) >= 4:
		return uint64(binary.BigEndian.Uint32(value[:4])), true
	default:
		return 0, false
	}
}

func parseAttrs(raw []byte) map[uint16][][]byte {
	out := make(map[uint16][][]byte)
	for len(raw) >= 4 {
		length := int(binary.LittleEndian.Uint16(raw[:2]))
		attrType := binary.LittleEndian.Uint16(raw[2:4]) & nlaTypeMask
		if length < 4 || length > len(raw) {
			break
		}
		value := raw[4:length]
		out[attrType] = append(out[attrType], value)
		aligned := (length + 3) &^ 3
		if aligned > len(raw) {
			break
		}
		raw = raw[aligned:]
	}
	return out
}

func firstAttr(attrs map[uint16][][]byte, attrType uint16) ([]byte, bool) {
	values := attrs[attrType]
	if len(values) == 0 {
		return nil, false
	}
	return values[0], true
}

func protocolName(value byte) string {
	switch value {
	case 6:
		return "tcp"
	case 17:
		return "udp"
	case 1:
		return "icmp"
	case 58:
		return "icmpv6"
	case 132:
		return "sctp"
	case 33:
		return "dccp"
	case 47:
		return "gre"
	default:
		return "other"
	}
}

func familyName(value byte) string {
	if value == syscall.AF_INET6 {
		return "ipv6"
	}
	return "ipv4"
}
