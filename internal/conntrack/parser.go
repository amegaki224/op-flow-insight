package conntrack

import (
	"bufio"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"strings"
)

type Tuple struct {
	Source      netip.Addr
	Destination netip.Addr
	SourcePort  uint16
	DestPort    uint16
	Packets     uint64
	Bytes       uint64
}

type Connection struct {
	Family   string
	Protocol string
	State    string
	Zone     uint16
	Original Tuple
	Reply    Tuple
	HasBytes bool
}

func (c Connection) Key() string {
	return fmt.Sprintf("%s|%d|%s|%s|%s|%d|%d",
		c.Family,
		c.Zone,
		c.Protocol,
		c.Original.Source,
		c.Original.Destination,
		c.Original.SourcePort,
		c.Original.DestPort,
	)
}

// LegacyKey allows active r10 baselines to migrate without importing the
// current byte counters again during an in-place upgrade.
func (c Connection) LegacyKey() string {
	return fmt.Sprintf("%s|%s|%s|%d|%d",
		c.Protocol,
		c.Original.Source,
		c.Original.Destination,
		c.Original.SourcePort,
		c.Original.DestPort,
	)
}

type Result struct {
	Connections []Connection
	Lines       int
	BadLines    int
	HasBytes    bool
}

func Parse(r io.Reader) Result {
	var result Result
	s := bufio.NewScanner(r)
	// Conntrack lines can be long when helper metadata is present.
	s.Buffer(make([]byte, 64*1024), 1024*1024)
	for s.Scan() {
		result.Lines++
		conn, ok := ParseLine(s.Text())
		if !ok {
			result.BadLines++
			continue
		}
		if conn.HasBytes {
			result.HasBytes = true
		}
		result.Connections = append(result.Connections, conn)
	}
	return result
}

func ParseLine(line string) (Connection, bool) {
	fields := strings.Fields(line)
	if len(fields) < 8 {
		return Connection{}, false
	}
	conn := Connection{Family: fields[0]}
	// Typical format is: ipv4 2 tcp 6 timeout STATE ...
	for _, candidate := range []string{"tcp", "udp", "icmp", "icmpv6", "sctp", "dccp", "gre"} {
		for i, field := range fields {
			if field == candidate {
				conn.Protocol = field
				if i+3 < len(fields) && !strings.Contains(fields[i+3], "=") {
					conn.State = fields[i+3]
				}
				break
			}
		}
		if conn.Protocol != "" {
			break
		}
	}
	if conn.Protocol == "" {
		return Connection{}, false
	}

	tuples := []*Tuple{&conn.Original, &conn.Reply}
	tupleIndex := -1
	byteFields := 0
	for _, field := range fields {
		key, value, found := strings.Cut(field, "=")
		if !found {
			continue
		}
		if key == "zone" {
			conn.Zone = parseUint16(value)
			continue
		}
		if key == "src" {
			tupleIndex++
			if tupleIndex >= len(tuples) {
				break
			}
		}
		if tupleIndex < 0 || tupleIndex >= len(tuples) {
			continue
		}
		tuple := tuples[tupleIndex]
		switch key {
		case "src":
			tuple.Source, _ = netip.ParseAddr(value)
		case "dst":
			tuple.Destination, _ = netip.ParseAddr(value)
		case "sport":
			tuple.SourcePort = parseUint16(value)
		case "dport":
			tuple.DestPort = parseUint16(value)
		case "packets":
			tuple.Packets, _ = strconv.ParseUint(value, 10, 64)
		case "bytes":
			tuple.Bytes, _ = strconv.ParseUint(value, 10, 64)
			byteFields++
		}
	}
	conn.HasBytes = byteFields >= 2
	if !conn.Original.Source.IsValid() || !conn.Original.Destination.IsValid() ||
		!conn.Reply.Source.IsValid() || !conn.Reply.Destination.IsValid() {
		return Connection{}, false
	}
	return conn, true
}

func parseUint16(value string) uint16 {
	n, _ := strconv.ParseUint(value, 10, 16)
	return uint16(n)
}
