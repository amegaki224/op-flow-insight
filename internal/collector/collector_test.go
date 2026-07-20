package collector

import (
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/op-flow-insight/op-flow-insight/internal/config"
	"github.com/op-flow-insight/op-flow-insight/internal/dataset"
	"github.com/op-flow-insight/op-flow-insight/internal/model"
)

func TestCumulativeDeltas(t *testing.T) {
	cfg := config.Default()
	cfg.StateFile = filepath.Join(t.TempDir(), "state.json")
	cfg.LANPrefixes = []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}
	data := dataset.NewManager(t.TempDir())
	tracker, err := New(cfg, data, "test")
	if err != nil {
		t.Fatal(err)
	}
	first := "ipv4 2 tcp 6 100 ESTABLISHED src=192.168.1.23 dst=1.1.1.1 sport=50000 dport=443 packets=1 bytes=100 src=1.1.1.1 dst=192.168.1.23 sport=443 dport=50000 packets=2 bytes=1000 [ASSURED]\n"
	second := "ipv4 2 tcp 6 98 ESTABLISHED src=192.168.1.23 dst=1.1.1.1 sport=50000 dport=443 packets=2 bytes=150 src=1.1.1.1 dst=192.168.1.23 sport=443 dport=50000 packets=3 bytes=1200 [ASSURED]\n"
	now := time.Now().UTC()
	tracker.Poll(strings.NewReader(first), now)
	tracker.Poll(strings.NewReader(second), now.Add(2*time.Second))
	got := tracker.Snapshot()
	if got.Totals.Uploaded != 150 || got.Totals.Downloaded != 1200 {
		t.Fatalf("unexpected totals: %+v", got.Totals)
	}
	if got.Totals.UploadBPS != 25 || got.Totals.DownloadBPS != 100 {
		t.Fatalf("unexpected rates: %+v", got.Totals)
	}
	if len(got.Flows) != 1 || got.Flows[0].Source.IP != "192.168.1.23" {
		t.Fatalf("unexpected flows: %+v", got.Flows)
	}
}

func TestDNATInbound(t *testing.T) {
	cfg := config.Default()
	cfg.StateFile = filepath.Join(t.TempDir(), "state.json")
	cfg.LANPrefixes = []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}
	tracker, _ := New(cfg, dataset.NewManager(t.TempDir()), "test")
	line := "ipv4 2 tcp 6 100 ESTABLISHED src=203.0.113.8 dst=198.51.100.10 sport=40000 dport=443 packets=4 bytes=800 src=192.168.1.50 dst=203.0.113.8 sport=8443 dport=40000 packets=3 bytes=600 [ASSURED]\n"
	tracker.Poll(strings.NewReader(line), time.Now().UTC())
	got := tracker.Snapshot()
	if len(got.Flows) != 1 || got.Flows[0].HostIP != "192.168.1.50" || got.Flows[0].Direction != "inbound" {
		t.Fatalf("unexpected flow: %+v", got.Flows)
	}
	if got.Totals.Uploaded != 600 || got.Totals.Downloaded != 800 {
		t.Fatalf("unexpected totals: %+v", got.Totals)
	}
}

func TestSnapshotHostsUseStableIPAddressOrder(t *testing.T) {
	cfg := config.Default()
	cfg.StateFile = filepath.Join(t.TempDir(), "state.json")
	tracker, err := New(cfg, dataset.NewManager(t.TempDir()), "test")
	if err != nil {
		t.Fatal(err)
	}
	tracker.hosts = map[string]model.Host{
		"192.168.1.100": {IP: "192.168.1.100", DownloadBPS: 9000, ActiveFlows: 1},
		"2001:db8::10":  {IP: "2001:db8::10", DownloadBPS: 8000, ActiveFlows: 1},
		"192.168.1.2":   {IP: "192.168.1.2", DownloadBPS: 1, ActiveFlows: 1},
		"2001:db8::2":   {IP: "2001:db8::2", DownloadBPS: 2, ActiveFlows: 1},
		"192.168.1.10":  {IP: "192.168.1.10", DownloadBPS: 10000, ActiveFlows: 1},
	}

	got := tracker.Snapshot()
	want := []string{
		"192.168.1.2",
		"192.168.1.10",
		"192.168.1.100",
		"2001:db8::2",
		"2001:db8::10",
	}
	if len(got.Hosts) != len(want) {
		t.Fatalf("host count = %d, want %d", len(got.Hosts), len(want))
	}
	for index, host := range got.Hosts {
		if host.IP != want[index] {
			t.Fatalf("host[%d] = %s, want %s", index, host.IP, want[index])
		}
	}
}

func TestIPv4AndIPv6MergeByMAC(t *testing.T) {
	cfg := config.Default()
	cfg.StateFile = filepath.Join(t.TempDir(), "state.json")
	cfg.LeasePath = filepath.Join(t.TempDir(), "missing.leases")
	cfg.LANPrefixes = []netip.Prefix{
		netip.MustParsePrefix("192.168.1.0/24"),
		netip.MustParsePrefix("2001:db8:64::/64"),
	}
	tracker, err := New(cfg, dataset.NewManager(t.TempDir()), "test")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	mac := "02:11:22:33:44:55"
	tracker.leases = map[string]lease{
		"192.168.1.23": {Hostname: "dual-stack-host", MAC: mac},
	}
	tracker.leaseByMAC = leasesByMAC(tracker.leases)
	tracker.neighbors = map[string]neighbor{
		"192.168.1.23": {
			IP: "192.168.1.23", MAC: mac, Device: "br-lan",
			State: "REACHABLE", Online: true,
		},
		"2001:db8:64::23": {
			IP: "2001:db8:64::23", MAC: mac, Device: "br-lan",
			State: "STALE", Online: true,
		},
	}
	tracker.lanDevices["br-lan"] = true
	tracker.lastLeaseRead = now

	fixture := strings.Join([]string{
		"ipv4 2 tcp 6 100 ESTABLISHED src=192.168.1.23 dst=1.1.1.1 sport=50000 dport=443 packets=1 bytes=100 src=1.1.1.1 dst=192.168.1.23 sport=443 dport=50000 packets=2 bytes=1000 [ASSURED]",
		"ipv6 10 tcp 6 100 ESTABLISHED src=2001:db8:64::23 dst=2606:4700:4700::1111 sport=50001 dport=443 packets=1 bytes=200 src=2606:4700:4700::1111 dst=2001:db8:64::23 sport=443 dport=50001 packets=2 bytes=2000 [ASSURED]",
	}, "\n") + "\n"
	tracker.Poll(strings.NewReader(fixture), now)

	got := tracker.Snapshot()
	if len(got.Hosts) != 1 {
		t.Fatalf("merged host count = %d, want 1: %+v", len(got.Hosts), got.Hosts)
	}
	host := got.Hosts[0]
	if host.ID != "mac:"+mac || host.Hostname != "dual-stack-host" {
		t.Fatalf("unexpected merged identity: %+v", host)
	}
	if host.Uploaded != 300 || host.Downloaded != 3000 {
		t.Fatalf("unexpected merged totals: %+v", host)
	}
	if len(host.Addresses) != 2 ||
		host.Addresses[0].Family != "ipv4" ||
		host.Addresses[1].Family != "ipv6" {
		t.Fatalf("unexpected merged addresses: %+v", host.Addresses)
	}
	if len(got.Flows) != 2 ||
		got.Flows[0].HostID != host.ID ||
		got.Flows[1].HostID != host.ID {
		t.Fatalf("flows were not linked to merged host: %+v", got.Flows)
	}
}

func TestOnlineHostOnlyShowsCurrentAddressesAfterSubnetMove(t *testing.T) {
	cfg := config.Default()
	cfg.StateFile = filepath.Join(t.TempDir(), "state.json")
	cfg.LeasePath = filepath.Join(t.TempDir(), "missing.leases")
	cfg.LANPrefixes = []netip.Prefix{
		netip.MustParsePrefix("192.168.10.0/24"),
		netip.MustParsePrefix("192.168.20.0/24"),
	}
	tracker, err := New(cfg, dataset.NewManager(t.TempDir()), "test")
	if err != nil {
		t.Fatal(err)
	}
	mac := "02:11:22:33:44:55"
	oldIP, currentIP := "192.168.10.23", "192.168.20.23"
	now := time.Now().UTC()
	tracker.leases = map[string]lease{
		oldIP: {
			IP: oldIP, Hostname: "moved-vm", MAC: mac,
			ExpiresAt: now.Add(time.Hour).Unix(),
		},
		currentIP: {
			IP: currentIP, Hostname: "moved-vm", MAC: mac,
			ExpiresAt: now.Add(2 * time.Hour).Unix(),
		},
	}
	tracker.leaseByMAC = leasesByMAC(tracker.leases)
	tracker.neighbors = map[string]neighbor{
		oldIP: {
			IP: oldIP, MAC: mac, Device: "br-lan",
			State: "STALE", Online: true,
		},
		currentIP: {
			IP: currentIP, MAC: mac, Device: "br-lan",
			State: "REACHABLE", Online: true,
		},
	}
	tracker.lanDevices["br-lan"] = true
	id := "mac:" + mac
	tracker.profiles[id] = model.HostProfile{
		ID: id, Hostname: "moved-vm", MAC: mac,
		Addresses: []model.HostAddress{
			hostAddress(netip.MustParseAddr(oldIP)),
			hostAddress(netip.MustParseAddr(currentIP)),
		},
	}

	if preferred := tracker.leaseByMAC[mac].IP; preferred != currentIP {
		t.Fatalf("preferred lease = %s, want %s", preferred, currentIP)
	}
	got := tracker.Snapshot()
	if len(got.Hosts) != 1 {
		t.Fatalf("host count = %d, want 1: %+v", len(got.Hosts), got.Hosts)
	}
	if len(got.Hosts[0].Addresses) != 1 ||
		got.Hosts[0].Addresses[0].IP != currentIP {
		t.Fatalf("inactive previous IP is still visible: %+v",
			got.Hosts[0].Addresses)
	}
	if len(tracker.profiles[id].Addresses) != 2 {
		t.Fatalf("retained profile lost historical addresses: %+v",
			tracker.profiles[id].Addresses)
	}

	// A live conntrack flow is direct evidence that an address is still in
	// active use, even when its neighbor entry has become STALE.
	tracker.hosts[oldIP] = model.Host{
		IP: oldIP, MAC: mac, ActiveFlows: 1, LastSeen: now,
	}
	got = tracker.Snapshot()
	if len(got.Hosts[0].Addresses) != 2 {
		t.Fatalf("actively used address was filtered out: %+v",
			got.Hosts[0].Addresses)
	}
}

func TestReadLeasesSkipsExpiredAddresses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dhcp.leases")
	mac := "02:11:22:33:44:55"
	currentExpiry := time.Now().Add(time.Hour).Unix()
	content := strings.Join([]string{
		"1 " + mac + " 192.168.10.23 moved-vm *",
		strconv.FormatInt(currentExpiry, 10) + " " + mac +
			" 192.168.20.23 moved-vm *",
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	leases := readLeases(path)
	if len(leases) != 1 {
		t.Fatalf("lease count = %d, want 1: %+v", len(leases), leases)
	}
	if _, found := leases["192.168.10.23"]; found {
		t.Fatalf("expired address was retained: %+v", leases)
	}
	if current := leasesByMAC(leases)[mac]; current.IP != "192.168.20.23" {
		t.Fatalf("current lease = %+v, want 192.168.20.23", current)
	}
}

func TestHostnameChangeRefreshesOnlineAndRetainedViews(t *testing.T) {
	cfg := config.Default()
	cfg.StateFile = filepath.Join(t.TempDir(), "state.json")
	cfg.LeasePath = filepath.Join(t.TempDir(), "dhcp.leases")
	cfg.LANPrefixes = []netip.Prefix{
		netip.MustParsePrefix("192.168.1.0/24"),
	}
	mac := "02:11:22:33:44:55"
	writeLease := func(hostname string) {
		t.Helper()
		line := "2000000000 " + mac + " 192.168.1.23 " + hostname + " *\n"
		if err := os.WriteFile(cfg.LeasePath, []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeLease("old-name")

	tracker, err := New(cfg, dataset.NewManager(t.TempDir()), "test")
	if err != nil {
		t.Fatal(err)
	}
	tracker.lanDevices["br-lan"] = true
	tracker.neighbors = map[string]neighbor{
		"192.168.1.23": {
			IP: "192.168.1.23", MAC: mac, Device: "br-lan",
			State: "REACHABLE", Online: true,
		},
	}

	now := time.Now().UTC()
	fixture := "ipv4 2 tcp 6 100 ESTABLISHED src=192.168.1.23 dst=1.1.1.1 sport=50000 dport=443 packets=1 bytes=100 src=1.1.1.1 dst=192.168.1.23 sport=443 dport=50000 packets=2 bytes=1000 [ASSURED]\n"
	tracker.Poll(strings.NewReader(fixture), now)
	if got := tracker.Snapshot(); len(got.Hosts) != 1 ||
		got.Hosts[0].Hostname != "old-name" {
		t.Fatalf("initial hostname was not loaded: %+v", got.Hosts)
	}

	writeLease("new-name")
	tracker.Poll(strings.NewReader(""), now.Add(leaseRefreshInterval))

	got := tracker.Snapshot()
	if len(got.Hosts) != 1 || got.Hosts[0].Hostname != "new-name" {
		t.Fatalf("online hostname was not refreshed: %+v", got.Hosts)
	}
	history, err := tracker.UsageHistory(
		"day", now.In(time.Local).Format("2006-01-02"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Records) != 1 ||
		history.Records[0].Hostname != "new-name" {
		t.Fatalf("retained hostname was not refreshed: %+v", history.Records)
	}
	_, exported, err := tracker.ExportUsageTXT(
		"day", now.In(time.Local).Format("2006-01-02"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(exported, "\tnew-name\t") ||
		strings.Contains(exported, "\told-name\t") {
		t.Fatalf("export did not use the current hostname:\n%s", exported)
	}
}

func TestOfflineHostIsHiddenAndUsageResumes(t *testing.T) {
	cfg := config.Default()
	cfg.StateFile = filepath.Join(t.TempDir(), "state.json")
	cfg.LeasePath = filepath.Join(t.TempDir(), "missing.leases")
	cfg.LANPrefixes = []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}
	tracker, err := New(cfg, dataset.NewManager(t.TempDir()), "test")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	first := "ipv4 2 tcp 6 100 ESTABLISHED src=192.168.1.50 dst=1.1.1.1 sport=50000 dport=443 packets=1 bytes=100 src=1.1.1.1 dst=192.168.1.50 sport=443 dport=50000 packets=2 bytes=1000 [ASSURED]\n"
	tracker.Poll(strings.NewReader(first), now)
	if got := tracker.Snapshot(); len(got.Hosts) != 1 {
		t.Fatalf("online host count = %d, want 1", len(got.Hosts))
	}

	tracker.Poll(strings.NewReader(""), now.Add(2*time.Second))
	if got := tracker.Snapshot(); len(got.Hosts) != 0 {
		t.Fatalf("offline hosts were not cleared from live view: %+v", got.Hosts)
	}
	if len(tracker.hosts) != 1 {
		t.Fatalf("offline accounting record was removed: %+v", tracker.hosts)
	}

	second := "ipv4 2 tcp 6 100 ESTABLISHED src=192.168.1.50 dst=9.9.9.9 sport=50001 dport=443 packets=1 bytes=50 src=9.9.9.9 dst=192.168.1.50 sport=443 dport=50001 packets=2 bytes=500 [ASSURED]\n"
	tracker.Poll(strings.NewReader(second), now.Add(4*time.Second))
	got := tracker.Snapshot()
	if len(got.Hosts) != 1 ||
		got.Hosts[0].Uploaded != 150 ||
		got.Hosts[0].Downloaded != 1500 {
		t.Fatalf("host did not resume its retained counters: %+v", got.Hosts)
	}
}

func TestMonthlyRolloverPreservesConnectionBaseline(t *testing.T) {
	cfg := config.Default()
	cfg.StateFile = filepath.Join(t.TempDir(), "state.json")
	cfg.LeasePath = filepath.Join(t.TempDir(), "missing.leases")
	cfg.LANPrefixes = []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}
	tracker, err := New(cfg, dataset.NewManager(t.TempDir()), "test")
	if err != nil {
		t.Fatal(err)
	}
	january := time.Date(2026, time.January, 31, 23, 59, 58, 0, time.Local)
	tracker.currentMonth = "2026-01"
	first := "ipv4 2 tcp 6 100 ESTABLISHED src=192.168.1.60 dst=1.1.1.1 sport=50000 dport=443 packets=1 bytes=1000 src=1.1.1.1 dst=192.168.1.60 sport=443 dport=50000 packets=2 bytes=10000 [ASSURED]\n"
	second := "ipv4 2 tcp 6 98 ESTABLISHED src=192.168.1.60 dst=1.1.1.1 sport=50000 dport=443 packets=2 bytes=1300 src=1.1.1.1 dst=192.168.1.60 sport=443 dport=50000 packets=3 bytes=12000 [ASSURED]\n"
	tracker.Poll(strings.NewReader(first), january)
	tracker.Poll(strings.NewReader(second), january.Add(4*time.Second))

	got := tracker.Snapshot()
	if got.Totals.Period != "2026-02" {
		t.Fatalf("current period = %q, want 2026-02", got.Totals.Period)
	}
	if got.Totals.Uploaded != 300 || got.Totals.Downloaded != 2000 {
		t.Fatalf("pre-midnight counters were recounted after reset: %+v", got.Totals)
	}
	januaryUsage := tracker.daily["2026-01-31"]["ip:192.168.1.60"]
	februaryUsage := tracker.daily["2026-02-01"]["ip:192.168.1.60"]
	if januaryUsage.Uploaded != 1000 || januaryUsage.Downloaded != 10000 ||
		februaryUsage.Uploaded != 300 || februaryUsage.Downloaded != 2000 {
		t.Fatalf("unexpected daily split: january=%+v february=%+v",
			januaryUsage, februaryUsage)
	}
}
