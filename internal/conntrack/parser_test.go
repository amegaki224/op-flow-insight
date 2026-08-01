package conntrack

import (
	"strings"
	"testing"
)

func TestParseTCP(t *testing.T) {
	line := "ipv4 2 tcp 6 431999 ESTABLISHED src=192.168.1.23 dst=1.1.1.1 sport=55123 dport=443 packets=10 bytes=1200 src=1.1.1.1 dst=192.168.1.23 sport=443 dport=55123 packets=12 bytes=8400 [ASSURED] mark=0 use=1"
	conn, ok := ParseLine(line)
	if !ok {
		t.Fatal("expected valid connection")
	}
	if conn.Protocol != "tcp" || !conn.HasBytes {
		t.Fatalf("unexpected protocol/bytes: %+v", conn)
	}
	if got := conn.Original.Source.String(); got != "192.168.1.23" {
		t.Fatalf("source = %s", got)
	}
	if conn.Original.Bytes != 1200 || conn.Reply.Bytes != 8400 {
		t.Fatalf("bytes = %d/%d", conn.Original.Bytes, conn.Reply.Bytes)
	}
}

func TestParseIPv6UDP(t *testing.T) {
	line := "ipv6 10 udp 17 20 src=fd00::20 dst=2606:4700:4700::1111 sport=50000 dport=53 packets=1 bytes=76 src=2606:4700:4700::1111 dst=fd00::20 sport=53 dport=50000 packets=1 bytes=128 mark=0 use=1"
	result := Parse(strings.NewReader(line + "\n"))
	if len(result.Connections) != 1 || !result.HasBytes {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestConnectionKeySeparatesFamilyAndZone(t *testing.T) {
	base := "ipv4 2 tcp 6 20 src=192.168.1.2 dst=1.1.1.1 sport=50000 dport=443 packets=1 bytes=10 src=1.1.1.1 dst=192.168.1.2 sport=443 dport=50000 packets=1 bytes=20 zone=7"
	conn, ok := ParseLine(base)
	if !ok {
		t.Fatal("expected valid connection")
	}
	if conn.Zone != 7 {
		t.Fatalf("zone = %d, want 7", conn.Zone)
	}
	other := conn
	other.Family = "ipv6"
	if conn.Key() == other.Key() {
		t.Fatal("address families shared a connection key")
	}
	other = conn
	other.Zone = 8
	if conn.Key() == other.Key() {
		t.Fatal("conntrack zones shared a connection key")
	}
	if conn.LegacyKey() == conn.Key() {
		t.Fatal("legacy and current key formats unexpectedly match")
	}
}
