package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/op-flow-insight/op-flow-insight/internal/model"
)

const lanPrefixRefreshInterval = 30 * time.Second

type ubusNetworkDump struct {
	Interfaces []ubusNetworkInterface `json:"interface"`
}

type ubusNetworkInterface struct {
	Name                 string        `json:"interface"`
	Device               string        `json:"device"`
	Up                   bool          `json:"up"`
	IPv4Addresses        []ubusAddress `json:"ipv4-address"`
	IPv6Addresses        []ubusAddress `json:"ipv6-address"`
	IPv6PrefixAssignment []ubusAddress `json:"ipv6-prefix-assignment"`
	Routes               []ubusRoute   `json:"route"`
}

type ubusAddress struct {
	Address string `json:"address"`
	Mask    int    `json:"mask"`
}

type ubusRoute struct {
	Target string `json:"target"`
	Mask   int    `json:"mask"`
}

func (t *Tracker) refreshLANPrefixes(now time.Time) {
	if !t.lastPrefixRead.IsZero() &&
		now.Sub(t.lastPrefixRead) < lanPrefixRefreshInterval {
		return
	}
	t.lastPrefixRead = now

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	output, err := exec.CommandContext(
		ctx, "ubus", "call", "network.interface", "dump",
	).Output()

	t.mu.Lock()
	defer t.mu.Unlock()
	t.localAddrs = localInterfaceAddrs()
	if err != nil {
		t.health.Warnings = uniqueWarnings(append(t.health.Warnings,
			"Unable to detect LAN prefixes through ubus; using configured prefixes: "+err.Error(),
		))
		return
	}
	prefixes, devices, routerAddresses, err := discoverLANDetails(
		output, t.cfg.LANPrefixes,
	)
	if err != nil {
		t.health.Warnings = uniqueWarnings(append(t.health.Warnings,
			"Unable to detect LAN prefixes through ubus; using configured prefixes: "+err.Error(),
		))
		return
	}
	t.health.Warnings = removeWarningPrefixes(t.health.Warnings,
		"Unable to detect LAN prefixes through ubus")
	t.lanPrefixes = prefixes
	t.lanDevices = devices
	t.routerLANAddresses = routerAddresses
}

func discoverLANPrefixes(
	raw []byte, configured []netip.Prefix,
) ([]netip.Prefix, error) {
	prefixes, _, _, err := discoverLANDetails(raw, configured)
	return prefixes, err
}

func discoverLANDetails(
	raw []byte, configured []netip.Prefix,
) ([]netip.Prefix, map[string]bool, []model.HostAddress, error) {
	var dump ubusNetworkDump
	if err := json.Unmarshal(raw, &dump); err != nil {
		return nil, nil, nil,
			fmt.Errorf("parse ubus network.interface dump: %w", err)
	}

	prefixes := append([]netip.Prefix(nil), configured...)
	devices := make(map[string]bool)
	var routerAddresses []model.HostAddress
	for _, iface := range dump.Interfaces {
		if !iface.Up || interfaceHasDefaultRoute(iface) || interfaceLooksLikeWAN(iface) {
			continue
		}
		if !interfaceMatchesLAN(iface, configured) {
			continue
		}
		if iface.Device != "" {
			devices[iface.Device] = true
		}
		for _, value := range append(
			append([]ubusAddress(nil), iface.IPv4Addresses...),
			iface.IPv6Addresses...,
		) {
			addr, parseErr := netip.ParseAddr(value.Address)
			if parseErr != nil {
				continue
			}
			routerAddresses = append(routerAddresses, hostAddress(addr))
		}
		for _, value := range append(
			append([]ubusAddress(nil), iface.IPv6PrefixAssignment...),
			iface.IPv6Addresses...,
		) {
			prefix, ok := ubusAddressPrefix(value)
			if !ok || !prefix.Addr().Is6() || prefix.Addr().IsLinkLocalUnicast() {
				continue
			}
			prefixes = append(prefixes, prefix)
		}
	}

	unique := make(map[string]netip.Prefix, len(prefixes))
	for _, prefix := range prefixes {
		masked := prefix.Masked()
		unique[masked.String()] = masked
	}
	prefixes = prefixes[:0]
	for _, prefix := range unique {
		prefixes = append(prefixes, prefix)
	}
	sort.Slice(prefixes, func(i, j int) bool {
		left, right := prefixes[i].Addr(), prefixes[j].Addr()
		if left.Is4() != right.Is4() {
			return left.Is4()
		}
		if comparison := left.Compare(right); comparison != 0 {
			return comparison < 0
		}
		return prefixes[i].Bits() < prefixes[j].Bits()
	})
	routerAddresses = uniqueHostAddresses(routerAddresses)
	return prefixes, devices, routerAddresses, nil
}

func interfaceMatchesLAN(
	iface ubusNetworkInterface, configured []netip.Prefix,
) bool {
	name := strings.ToLower(iface.Name)
	device := strings.ToLower(iface.Device)
	if name == "lan" || strings.HasPrefix(name, "lan_") ||
		device == "br-lan" || strings.HasPrefix(device, "br-lan.") {
		return true
	}
	for _, value := range append(
		append([]ubusAddress(nil), iface.IPv4Addresses...),
		iface.IPv6Addresses...,
	) {
		addr, err := netip.ParseAddr(value.Address)
		if err != nil {
			continue
		}
		for _, prefix := range configured {
			if prefix.Contains(addr) {
				return true
			}
		}
	}
	return false
}

func interfaceHasDefaultRoute(iface ubusNetworkInterface) bool {
	for _, route := range iface.Routes {
		if route.Mask != 0 {
			continue
		}
		addr, err := netip.ParseAddr(route.Target)
		if err == nil && addr.IsUnspecified() {
			return true
		}
	}
	return false
}

func interfaceLooksLikeWAN(iface ubusNetworkInterface) bool {
	name := strings.ToLower(iface.Name)
	return name == "wan" || name == "wan6" ||
		strings.HasPrefix(name, "wan_") || strings.HasPrefix(name, "wan.")
}

func ubusAddressPrefix(value ubusAddress) (netip.Prefix, bool) {
	addr, err := netip.ParseAddr(value.Address)
	if err != nil || value.Mask < 0 || value.Mask > addr.BitLen() {
		return netip.Prefix{}, false
	}
	return netip.PrefixFrom(addr, value.Mask).Masked(), true
}
