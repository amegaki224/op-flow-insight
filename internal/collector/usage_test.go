package collector

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/op-flow-insight/op-flow-insight/internal/model"
)

func TestUsageHistoryAggregatesDayMonthQuarterAndYear(t *testing.T) {
	tracker := &Tracker{
		daily: map[string]map[string]model.TrafficUsage{
			"2026-01-31": {
				"mac:02:11:22:33:44:55": {Downloaded: 100, Uploaded: 10},
			},
			"2026-02-01": {
				"mac:02:11:22:33:44:55": {Downloaded: 200, Uploaded: 20},
			},
			"2026-04-01": {
				"mac:02:11:22:33:44:55": {Downloaded: 400, Uploaded: 40},
			},
		},
		profiles: map[string]model.HostProfile{
			"mac:02:11:22:33:44:55": {
				ID: "mac:02:11:22:33:44:55", Hostname: "history-host",
				MAC: "02:11:22:33:44:55",
				Addresses: []model.HostAddress{
					{IP: "192.168.1.23", Family: "ipv4", Scope: "lan"},
					{IP: "fd00::23", Family: "ipv6", Scope: "lan"},
				},
			},
		},
		aliases: make(map[string]string),
	}
	cases := []struct {
		granularity string
		period      string
		downloaded  uint64
		uploaded    uint64
	}{
		{"day", "2026-02-01", 200, 20},
		{"month", "2026-02", 200, 20},
		{"quarter", "2026-Q1", 300, 30},
		{"year", "2026", 700, 70},
	}
	for _, tc := range cases {
		history, err := tracker.UsageHistory(tc.granularity, tc.period)
		if err != nil {
			t.Fatalf("%s history failed: %v", tc.granularity, err)
		}
		if len(history.Records) != 1 ||
			history.Totals.Downloaded != tc.downloaded ||
			history.Totals.Uploaded != tc.uploaded {
			t.Fatalf("%s history = %+v", tc.granularity, history)
		}
	}
}

func TestUsageExportIsUTF8TabSeparatedTXT(t *testing.T) {
	tracker := &Tracker{
		daily: map[string]map[string]model.TrafficUsage{
			"2026-07-20": {
				"ip:192.168.1.2": {Downloaded: 2048, Uploaded: 1024},
			},
		},
		profiles: map[string]model.HostProfile{
			"ip:192.168.1.2": {
				ID: "ip:192.168.1.2", Hostname: "test-host",
				Addresses: []model.HostAddress{
					{IP: "192.168.1.2", Family: "ipv4", Scope: "lan"},
				},
			},
		},
		aliases: make(map[string]string),
	}
	filename, content, err := tracker.ExportUsageTXT("day", "2026-07-20")
	if err != nil {
		t.Fatal(err)
	}
	if filename != "op-flow-day-2026-07-20.txt" {
		t.Fatalf("filename = %q", filename)
	}
	if !strings.HasPrefix(content, "\uFEFFPeriod\tHost\t") ||
		!strings.Contains(content, "test-host\t\t192.168.1.2") ||
		!strings.Contains(content, "2.00 KiB\t1.00 KiB") {
		t.Fatalf("unexpected TXT export:\n%s", content)
	}
}

func TestHostAddressScopes(t *testing.T) {
	tests := map[string]model.HostAddress{
		"192.168.1.2": {IP: "192.168.1.2", Family: "ipv4", Scope: "lan"},
		"fd00::2":     {IP: "fd00::2", Family: "ipv6", Scope: "lan"},
		"fe80::2":     {IP: "fe80::2", Family: "ipv6", Scope: "link-local"},
		"2001:db8::2": {IP: "2001:db8::2", Family: "ipv6", Scope: "global"},
	}
	for raw, want := range tests {
		got := hostAddress(mustAddr(t, raw))
		if got != want {
			t.Fatalf("hostAddress(%s) = %+v, want %+v", raw, got, want)
		}
	}
}

func mustAddr(t *testing.T, raw string) netip.Addr {
	t.Helper()
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		t.Fatal(err)
	}
	return addr
}

func TestPeriodUsesLocalMachineTime(t *testing.T) {
	original := time.Local
	time.Local = time.FixedZone("test", 9*60*60)
	t.Cleanup(func() { time.Local = original })
	value := time.Date(2026, time.March, 31, 15, 30, 0, 0, time.UTC)
	if got := formatPeriod("day", value.In(time.Local)); got != "2026-04-01" {
		t.Fatalf("local day = %s, want 2026-04-01", got)
	}
}
