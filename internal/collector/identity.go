package collector

import (
	"context"
	"net/netip"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/op-flow-insight/op-flow-insight/internal/model"
)

const neighborRefreshInterval = 10 * time.Second

type neighbor struct {
	IP     string
	MAC    string
	Device string
	State  string
	Online bool
}

func (t *Tracker) refreshNeighbors(now time.Time) {
	if !t.lastNeighborRead.IsZero() &&
		now.Sub(t.lastNeighborRead) < neighborRefreshInterval {
		return
	}
	t.lastNeighborRead = now
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, "ip", "neigh", "show").Output()
	if err != nil {
		return
	}
	entries := parseNeighbors(raw)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.neighbors = entries
	for ip, entry := range entries {
		addr, parseErr := netip.ParseAddr(ip)
		if parseErr != nil || !entry.Online || !t.neighborIsLANLocked(addr, entry) {
			continue
		}
		id, hostname, mac := t.resolveIdentityLocked(ip)
		host := model.Host{IP: ip, Hostname: hostname, MAC: mac, LastSeen: now}
		t.rememberProfileLocked(id, host, addr, now)
	}
}

func parseNeighbors(raw []byte) map[string]neighbor {
	out := make(map[string]neighbor)
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		addr, err := netip.ParseAddr(fields[0])
		if err != nil {
			continue
		}
		entry := neighbor{IP: addr.String()}
		for index := 1; index < len(fields); index++ {
			switch fields[index] {
			case "dev":
				if index+1 < len(fields) {
					entry.Device = fields[index+1]
					index++
				}
			case "lladdr":
				if index+1 < len(fields) {
					entry.MAC = normalizeMAC(fields[index+1])
					index++
				}
			default:
				value := strings.ToUpper(fields[index])
				switch value {
				case "REACHABLE", "STALE", "DELAY", "PROBE",
					"PERMANENT", "NOARP", "FAILED", "INCOMPLETE":
					entry.State = value
				}
			}
		}
		entry.Online = entry.MAC != "" &&
			entry.State != "FAILED" && entry.State != "INCOMPLETE"
		out[entry.IP] = entry
	}
	return out
}

func (t *Tracker) neighborIsLANLocked(addr netip.Addr, entry neighbor) bool {
	if t.localAddrs[addr] {
		return false
	}
	if entry.Device != "" && t.lanDevices[entry.Device] {
		return true
	}
	return !addr.IsLinkLocalUnicast() && t.isLAN(addr)
}

func leasesByMAC(leases map[string]lease) map[string]lease {
	out := make(map[string]lease)
	for _, item := range leases {
		mac := normalizeMAC(item.MAC)
		if mac == "" {
			continue
		}
		current := out[mac]
		if current.Hostname == "" || item.Hostname != "" {
			out[mac] = item
		}
	}
	return out
}

func normalizeMAC(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "" || value == "00:00:00:00:00:00" {
		return ""
	}
	return value
}

func identity(mac, ip string) string {
	if normalized := normalizeMAC(mac); normalized != "" {
		return "mac:" + normalized
	}
	return "ip:" + ip
}

func (t *Tracker) resolveIdentityLocked(ip string) (string, string, string) {
	hostname, mac := "", ""
	if info, found := t.leases[ip]; found {
		hostname, mac = info.Hostname, normalizeMAC(info.MAC)
	}
	if entry, found := t.neighbors[ip]; found && entry.MAC != "" {
		mac = normalizeMAC(entry.MAC)
	}
	if host, found := t.hosts[ip]; found {
		if hostname == "" {
			hostname = host.Hostname
		}
		if mac == "" {
			mac = normalizeMAC(host.MAC)
		}
	}
	if info, found := t.leaseByMAC[mac]; found && hostname == "" {
		hostname = info.Hostname
	}
	id := identity(mac, ip)
	if mac != "" {
		t.aliases["ip:"+ip] = id
	}
	id = t.canonicalIDLocked(id)
	if profile, found := t.profiles[id]; found {
		if hostname == "" {
			hostname = profile.Hostname
		}
		if mac == "" {
			mac = profile.MAC
		}
	}
	return id, hostname, mac
}

func (t *Tracker) identityForIPLocked(ip string) string {
	mac := ""
	if info, found := t.leases[ip]; found {
		mac = info.MAC
	}
	if entry, found := t.neighbors[ip]; found && entry.MAC != "" {
		mac = entry.MAC
	}
	if host, found := t.hosts[ip]; found && mac == "" {
		mac = host.MAC
	}
	return t.canonicalIDLocked(identity(mac, ip))
}

func (t *Tracker) canonicalIDLocked(id string) string {
	seen := make(map[string]bool)
	for id != "" && !seen[id] {
		seen[id] = true
		next := t.aliases[id]
		if next == "" || next == id {
			break
		}
		id = next
	}
	return id
}

func (t *Tracker) rememberProfileLocked(
	id string, host model.Host, addr netip.Addr, now time.Time,
) {
	id = t.canonicalIDLocked(id)
	profile := t.profiles[id]
	profile.ID = id
	if host.Hostname != "" {
		profile.Hostname = host.Hostname
	}
	if mac := normalizeMAC(host.MAC); mac != "" {
		profile.MAC = mac
	}
	profile.Addresses = uniqueHostAddresses(append(
		profile.Addresses, hostAddress(addr),
	))
	if now.After(profile.LastSeen) {
		profile.LastSeen = now
	}
	t.profiles[id] = profile
}

func hostAddress(addr netip.Addr) model.HostAddress {
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

func addressFamily(addr netip.Addr) string {
	if addr.Unmap().Is4() {
		return "ipv4"
	}
	return "ipv6"
}

func uniqueHostAddresses(items []model.HostAddress) []model.HostAddress {
	unique := make(map[string]model.HostAddress)
	for _, item := range items {
		addr, err := netip.ParseAddr(item.IP)
		if err != nil {
			continue
		}
		value := hostAddress(addr)
		unique[value.IP] = value
	}
	out := make([]model.HostAddress, 0, len(unique))
	for _, item := range unique {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		left := netip.MustParseAddr(out[i].IP).Unmap()
		right := netip.MustParseAddr(out[j].IP).Unmap()
		if left.Is4() != right.Is4() {
			return left.Is4()
		}
		return left.Compare(right) < 0
	})
	return out
}

func (t *Tracker) onlineHostsLocked() []model.Host {
	active := make(map[string]bool)
	for ip, host := range t.hosts {
		if host.ActiveFlows > 0 {
			active[t.identityForIPLocked(ip)] = true
		}
	}
	for ip, entry := range t.neighbors {
		addr, err := netip.ParseAddr(ip)
		if err == nil && entry.Online && t.neighborIsLANLocked(addr, entry) {
			active[t.identityForIPLocked(ip)] = true
		}
	}

	groups := make(map[string]model.Host, len(active))
	for id := range active {
		profile := t.profiles[t.canonicalIDLocked(id)]
		groups[id] = model.Host{
			ID: id, Hostname: profile.Hostname, MAC: profile.MAC, Online: true,
		}
	}
	for ip, host := range t.hosts {
		id := t.identityForIPLocked(ip)
		if !active[id] {
			continue
		}
		group := groups[id]
		group.ID = id
		group.Online = true
		if host.Hostname != "" {
			group.Hostname = host.Hostname
		}
		if host.MAC != "" {
			group.MAC = normalizeMAC(host.MAC)
		}
		group.Uploaded += host.Uploaded
		group.Downloaded += host.Downloaded
		group.UploadBPS += host.UploadBPS
		group.DownloadBPS += host.DownloadBPS
		group.ActiveFlows += host.ActiveFlows
		if host.MaxRisk > group.MaxRisk {
			group.MaxRisk = host.MaxRisk
		}
		if host.LastSeen.After(group.LastSeen) {
			group.LastSeen = host.LastSeen
		}
		if addr, err := netip.ParseAddr(ip); err == nil &&
			(host.ActiveFlows > 0 || t.neighborOnlineLocked(ip)) {
			group.Addresses = append(group.Addresses, hostAddress(addr))
		}
		groups[id] = group
	}
	for ip, entry := range t.neighbors {
		addr, err := netip.ParseAddr(ip)
		if err != nil || !entry.Online || !t.neighborIsLANLocked(addr, entry) {
			continue
		}
		id := t.identityForIPLocked(ip)
		if !active[id] {
			continue
		}
		group := groups[id]
		group.ID = id
		group.Online = true
		if group.MAC == "" {
			group.MAC = normalizeMAC(entry.MAC)
		}
		if group.Hostname == "" {
			if info, found := t.leaseByMAC[group.MAC]; found {
				group.Hostname = info.Hostname
			}
		}
		group.Addresses = append(group.Addresses, hostAddress(addr))
		groups[id] = group
	}
	for ip, info := range t.leases {
		id := t.canonicalIDLocked(identity(info.MAC, ip))
		if !active[id] {
			continue
		}
		group := groups[id]
		if group.Hostname == "" {
			group.Hostname = info.Hostname
		}
		if group.MAC == "" {
			group.MAC = normalizeMAC(info.MAC)
		}
		if addr, err := netip.ParseAddr(ip); err == nil {
			group.Addresses = append(group.Addresses, hostAddress(addr))
		}
		groups[id] = group
	}

	hosts := make([]model.Host, 0, len(groups))
	for _, host := range groups {
		host.Addresses = uniqueHostAddresses(host.Addresses)
		if len(host.Addresses) > 0 {
			host.IP = host.Addresses[0].IP
		}
		hosts = append(hosts, host)
	}
	sort.Slice(hosts, func(i, j int) bool {
		return compareHostAddress(hosts[i].IP, hosts[j].IP) < 0
	})
	return hosts
}

func (t *Tracker) neighborOnlineLocked(ip string) bool {
	entry, found := t.neighbors[ip]
	return found && entry.Online
}

func compareHostAddress(leftRaw, rightRaw string) int {
	left, leftErr := netip.ParseAddr(leftRaw)
	right, rightErr := netip.ParseAddr(rightRaw)
	if leftErr != nil || rightErr != nil {
		return strings.Compare(leftRaw, rightRaw)
	}
	left, right = left.Unmap(), right.Unmap()
	if left.Is4() != right.Is4() {
		if left.Is4() {
			return -1
		}
		return 1
	}
	return left.Compare(right)
}

func (t *Tracker) snapshotFlowsLocked() []model.Flow {
	flows := append([]model.Flow(nil), t.flows...)
	for index := range flows {
		flows[index].HostID = t.identityForIPLocked(flows[index].HostIP)
		if flows[index].IPVersion == "" {
			if addr, err := netip.ParseAddr(flows[index].HostIP); err == nil {
				flows[index].IPVersion = addressFamily(addr)
			}
		}
	}
	return flows
}
