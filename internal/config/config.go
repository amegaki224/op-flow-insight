package config

import (
	"bufio"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Enabled        bool
	LANPrefixes    []netip.Prefix
	PollInterval   time.Duration
	SaveInterval   time.Duration
	MaxFlows       int
	ConntrackPath  string
	LeasePath      string
	DHCPConfigPath string
	StateFile      string
	SocketPath     string
	DataDir        string
	AutoUpdate     bool
	UpdateInterval time.Duration
}

func Default() Config {
	prefixes := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"fd00::/8",
	}
	cfg := Config{
		Enabled:        true,
		PollInterval:   2 * time.Second,
		SaveInterval:   5 * time.Minute,
		MaxFlows:       500,
		ConntrackPath:  "/proc/net/nf_conntrack",
		LeasePath:      "/tmp/dhcp.leases",
		DHCPConfigPath: "/etc/config/dhcp",
		StateFile:      "/etc/op-flow/state.json",
		SocketPath:     "/var/run/op-flow.sock",
		DataDir:        "/usr/share/op-flow/data",
		AutoUpdate:     true,
		UpdateInterval: 24 * time.Hour,
	}
	for _, raw := range prefixes {
		p, _ := netip.ParsePrefix(raw)
		cfg.LANPrefixes = append(cfg.LANPrefixes, p)
	}
	return cfg
}

// Load parses the small UCI subset used by /etc/config/op-flow. It deliberately
// avoids shelling out to uci so the same binary is easy to test and recover.
func Load(path string) (Config, error) {
	cfg := Default()
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	defer f.Close()

	var explicitPrefixes []netip.Prefix
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "config ") {
			continue
		}
		parts := splitUCI(line)
		if len(parts) < 3 {
			continue
		}
		kind, key, value := parts[0], parts[1], parts[2]
		if kind == "list" && key == "lan_cidr" {
			p, parseErr := netip.ParsePrefix(value)
			if parseErr != nil {
				return cfg, fmt.Errorf("invalid lan_cidr %q: %w", value, parseErr)
			}
			explicitPrefixes = append(explicitPrefixes, p.Masked())
			continue
		}
		if kind != "option" {
			continue
		}
		switch key {
		case "enabled":
			cfg.Enabled = parseBool(value, cfg.Enabled)
		case "poll_interval":
			cfg.PollInterval = parseDuration(value, cfg.PollInterval)
		case "save_interval":
			cfg.SaveInterval = parseDuration(value, cfg.SaveInterval)
		case "max_flows":
			if n, parseErr := strconv.Atoi(value); parseErr == nil && n >= 10 && n <= 10000 {
				cfg.MaxFlows = n
			}
		case "conntrack_path":
			cfg.ConntrackPath = value
		case "lease_path":
			cfg.LeasePath = value
		case "dhcp_config_path":
			cfg.DHCPConfigPath = value
		case "state_file":
			cfg.StateFile = value
		case "socket_path":
			cfg.SocketPath = value
		case "data_dir":
			cfg.DataDir = value
		case "auto_update":
			cfg.AutoUpdate = parseBool(value, cfg.AutoUpdate)
		case "update_interval":
			cfg.UpdateInterval = parseDuration(value, cfg.UpdateInterval)
		}
	}
	if err := s.Err(); err != nil {
		return cfg, err
	}
	if len(explicitPrefixes) > 0 {
		cfg.LANPrefixes = explicitPrefixes
	}
	if cfg.PollInterval < 500*time.Millisecond {
		cfg.PollInterval = 500 * time.Millisecond
	}
	if cfg.SaveInterval < 30*time.Second {
		cfg.SaveInterval = 30 * time.Second
	}
	if cfg.UpdateInterval < time.Hour {
		cfg.UpdateInterval = time.Hour
	}
	return cfg, nil
}

func splitUCI(line string) []string {
	var out []string
	for len(line) > 0 {
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if line[0] == '\'' || line[0] == '"' {
			q := line[0]
			line = line[1:]
			i := strings.IndexByte(line, q)
			if i < 0 {
				out = append(out, line)
				break
			}
			out = append(out, line[:i])
			line = line[i+1:]
			continue
		}
		i := strings.IndexAny(line, " \t")
		if i < 0 {
			out = append(out, line)
			break
		}
		out = append(out, line[:i])
		line = line[i+1:]
	}
	return out
}

func parseBool(value string, fallback bool) bool {
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func parseDuration(value string, fallback time.Duration) time.Duration {
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}
