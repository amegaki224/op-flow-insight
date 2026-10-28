package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "op-flow")
	body := "config op-flow 'main'\n" +
		"\toption enabled '0'\n" +
		"\tlist lan_cidr '192.168.77.0/24'\n" +
		"\toption poll_interval '1500ms'\n" +
		"\toption dhcp_config_path '/tmp/test-dhcp'\n" +
		"\toption max_flows '900'\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled {
		t.Fatal("enabled should be false")
	}
	if got := cfg.PollInterval; got != 1500*time.Millisecond {
		t.Fatalf("poll interval = %s", got)
	}
	if len(cfg.LANPrefixes) != 1 || cfg.LANPrefixes[0].String() != "192.168.77.0/24" {
		t.Fatalf("unexpected prefixes: %v", cfg.LANPrefixes)
	}
	if cfg.MaxFlows != 900 {
		t.Fatalf("max flows = %d", cfg.MaxFlows)
	}
	if cfg.DHCPConfigPath != "/tmp/test-dhcp" {
		t.Fatalf("DHCP config path = %q", cfg.DHCPConfigPath)
	}
}
