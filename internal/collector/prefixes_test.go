package collector

import (
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/op-flow-insight/op-flow-insight/internal/config"
	"github.com/op-flow-insight/op-flow-insight/internal/dataset"
)

func TestDiscoverLANPrefixesIncludesDelegatedGlobalIPv6(t *testing.T) {
	configured := []netip.Prefix{
		netip.MustParsePrefix("192.168.0.0/16"),
		netip.MustParsePrefix("fd00::/8"),
	}
	raw := []byte(`{
		"interface": [
			{
				"interface": "lan",
				"device": "br-lan",
				"up": true,
				"ipv4-address": [{"address": "192.168.1.1", "mask": 24}],
				"ipv6-address": [{"address": "2001:db8:64::1", "mask": 64}],
				"ipv6-prefix-assignment": [
					{"address": "2001:db8:64::", "mask": 64}
				],
				"route": []
			},
			{
				"interface": "wan",
				"device": "eth0",
				"up": true,
				"ipv4-address": [{"address": "192.168.100.2", "mask": 24}],
				"ipv6-prefix-assignment": [
					{"address": "2001:db8:ffff::", "mask": 64}
				],
				"route": [{"target": "0.0.0.0", "mask": 0}]
			}
		]
	}`)

	prefixes, devices, routerAddresses, err := discoverLANDetails(raw, configured)
	if err != nil {
		t.Fatal(err)
	}
	if !containsPrefix(prefixes, "2001:db8:64::/64") {
		t.Fatalf("delegated LAN IPv6 prefix not discovered: %v", prefixes)
	}
	if containsPrefix(prefixes, "2001:db8:ffff::/64") {
		t.Fatalf("WAN IPv6 prefix incorrectly classified as LAN: %v", prefixes)
	}
	if !devices["br-lan"] {
		t.Fatalf("LAN device was not discovered: %v", devices)
	}
	if len(routerAddresses) != 2 ||
		routerAddresses[0].IP != "192.168.1.1" ||
		routerAddresses[1].IP != "2001:db8:64::1" {
		t.Fatalf("router LAN addresses were not identified: %+v", routerAddresses)
	}
}

func TestDelegatedGlobalIPv6TrafficIsAccounted(t *testing.T) {
	cfg := config.Default()
	cfg.StateFile = filepath.Join(t.TempDir(), "state.json")
	tracker, err := New(cfg, dataset.NewManager(t.TempDir()), "test")
	if err != nil {
		t.Fatal(err)
	}
	tracker.lanPrefixes = append(
		tracker.lanPrefixes,
		netip.MustParsePrefix("2001:db8:64::/64"),
	)

	first := "ipv6 10 tcp 6 100 ESTABLISHED src=2001:db8:64::20 dst=2606:4700:4700::1111 sport=50000 dport=443 packets=10 bytes=1000 src=2606:4700:4700::1111 dst=2001:db8:64::20 sport=443 dport=50000 packets=20 bytes=8000 [ASSURED]\n"
	second := "ipv6 10 tcp 6 98 ESTABLISHED src=2001:db8:64::20 dst=2606:4700:4700::1111 sport=50000 dport=443 packets=15 bytes=3000 src=2606:4700:4700::1111 dst=2001:db8:64::20 sport=443 dport=50000 packets=40 bytes=18000 [ASSURED]\n"
	now := time.Now().UTC()
	tracker.Poll(strings.NewReader(first), now)
	tracker.Poll(strings.NewReader(second), now.Add(2*time.Second))

	got := tracker.Snapshot()
	if got.Totals.Uploaded != 3000 || got.Totals.Downloaded != 18000 {
		t.Fatalf("unexpected IPv6 totals: %+v", got.Totals)
	}
	if got.Totals.UploadBPS != 1000 || got.Totals.DownloadBPS != 5000 {
		t.Fatalf("unexpected IPv6 rates: %+v", got.Totals)
	}
	if len(got.Flows) != 1 || got.Flows[0].HostIP != "2001:db8:64::20" {
		t.Fatalf("unexpected IPv6 flows: %+v", got.Flows)
	}
}

func containsPrefix(prefixes []netip.Prefix, expected string) bool {
	want := netip.MustParsePrefix(expected)
	for _, prefix := range prefixes {
		if prefix == want {
			return true
		}
	}
	return false
}
