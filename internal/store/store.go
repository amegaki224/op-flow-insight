package store

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/op-flow-insight/op-flow-insight/internal/model"
)

const CurrentVersion = 2

type Baseline struct {
	HostIP   string    `json:"host_ip"`
	Uploaded uint64    `json:"uploaded"`
	Download uint64    `json:"downloaded"`
	LastSeen time.Time `json:"last_seen"`
}

type State struct {
	Version      int                                      `json:"version"`
	SavedAt      time.Time                                `json:"saved_at"`
	Hosts        map[string]model.Host                    `json:"hosts"`
	Active       map[string]Baseline                      `json:"active"`
	History      []model.RatePoint                        `json:"history,omitempty"`
	Daily        map[string]map[string]model.TrafficUsage `json:"daily,omitempty"`
	Profiles     map[string]model.HostProfile             `json:"profiles,omitempty"`
	Aliases      map[string]string                        `json:"aliases,omitempty"`
	CurrentMonth string                                   `json:"current_month,omitempty"`
}

func Empty() State {
	return State{
		Version:  CurrentVersion,
		Hosts:    make(map[string]model.Host),
		Active:   make(map[string]Baseline),
		Daily:    make(map[string]map[string]model.TrafficUsage),
		Profiles: make(map[string]model.HostProfile),
		Aliases:  make(map[string]string),
	}
}

func Load(path string) (State, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Empty(), nil
		}
		return Empty(), err
	}
	var state State
	if err := json.Unmarshal(raw, &state); err != nil {
		return Empty(), err
	}
	switch state.Version {
	case 1:
		migrateV1(&state)
	case CurrentVersion:
	default:
		return Empty(), &UnsupportedVersionError{Version: state.Version}
	}
	if state.Hosts == nil {
		state.Hosts = make(map[string]model.Host)
	}
	if state.Active == nil {
		state.Active = make(map[string]Baseline)
	}
	if state.Daily == nil {
		state.Daily = make(map[string]map[string]model.TrafficUsage)
	}
	if state.Profiles == nil {
		state.Profiles = make(map[string]model.HostProfile)
	}
	if state.Aliases == nil {
		state.Aliases = make(map[string]string)
	}
	return state, nil
}

type UnsupportedVersionError struct {
	Version int
}

func (e *UnsupportedVersionError) Error() string {
	return fmt.Sprintf("unsupported state version %d", e.Version)
}

func migrateV1(state *State) {
	state.Version = CurrentVersion
	state.Daily = make(map[string]map[string]model.TrafficUsage)
	state.Profiles = make(map[string]model.HostProfile)
	state.Aliases = make(map[string]string)
	when := state.SavedAt
	if when.IsZero() {
		when = time.Now()
	}
	local := when.In(time.Local)
	day := local.Format("2006-01-02")
	state.CurrentMonth = local.Format("2006-01")
	state.Daily[day] = make(map[string]model.TrafficUsage)
	for ip, host := range state.Hosts {
		id := identity(host.MAC, ip)
		usage := state.Daily[day][id]
		usage.Uploaded += host.Uploaded
		usage.Downloaded += host.Downloaded
		state.Daily[day][id] = usage
		profile := state.Profiles[id]
		profile.ID = id
		if host.Hostname != "" {
			profile.Hostname = host.Hostname
		}
		if mac := normalizeMAC(host.MAC); mac != "" {
			profile.MAC = mac
		}
		if !containsAddress(profile.Addresses, ip) {
			profile.Addresses = append(profile.Addresses, address(ip))
		}
		if host.LastSeen.After(profile.LastSeen) {
			profile.LastSeen = host.LastSeen
		}
		state.Profiles[id] = profile
		if host.MAC != "" {
			state.Aliases["ip:"+ip] = id
		}
	}
}

func identity(mac, ip string) string {
	if normalized := normalizeMAC(mac); normalized != "" {
		return "mac:" + normalized
	}
	return "ip:" + ip
}

func normalizeMAC(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "00:00:00:00:00:00" {
		return ""
	}
	return value
}

func address(ip string) model.HostAddress {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		family := "ipv4"
		if strings.Contains(ip, ":") {
			family = "ipv6"
		}
		return model.HostAddress{IP: ip, Family: family}
	}
	addr = addr.Unmap()
	if addr.Is4() {
		return model.HostAddress{IP: addr.String(), Family: "ipv4", Scope: "lan"}
	}
	scope := "global"
	switch {
	case addr.IsLinkLocalUnicast():
		scope = "link-local"
	case addr.IsPrivate():
		scope = "lan"
	}
	return model.HostAddress{IP: addr.String(), Family: "ipv6", Scope: scope}
}

func containsAddress(addresses []model.HostAddress, ip string) bool {
	for _, value := range addresses {
		if value.IP == ip {
			return true
		}
	}
	return false
}

func Save(path string, state State) error {
	state.Version = CurrentVersion
	state.SavedAt = time.Now().UTC()
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err == nil {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(tmpPath, path)
}
