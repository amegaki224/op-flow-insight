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
	for ip, item := range leases {
		mac := normalizeMAC(item.MAC)
		if mac == "" {
			continue
		}
		if item.IP == "" {
			item.IP = ip
		}
		current := out[mac]
		if preferLease(item, current) {
			out[mac] = item
		}
	}
	return out
}

func preferLease(candidate, current lease) bool {
	if current.IP == "" {
		return true
	}
	// A zero expiry is a static/infinite dnsmasq lease.
	if candidate.ExpiresAt == 0 || current.ExpiresAt == 0 {
		if candidate.ExpiresAt != current.ExpiresAt {
			return candidate.ExpiresAt == 0
		}
	} else if candidate.ExpiresAt != current.ExpiresAt {
		return candidate.ExpiresAt > current.ExpiresAt
	}
	if (candidate.Hostname != "") != (current.Hostname != "") {
		return candidate.Hostname != ""
	}
	return compareHostAddress(candidate.IP, current.IP) > 0
}

func (t *Tracker) refreshLeasesLocked(now time.Time) {
	t.leases = readLeases(t.cfg.LeasePath)
	t.leaseByMAC = leasesByMAC(t.leases)
	t.lastLeaseRead = now
	t.applyLeaseHostnamesLocked()
}

func (t *Tracker) applyLeaseHostnamesLocked() {
	names := make(map[string]string)
	macs := make(map[string]string)
	for ip, info := range t.leases {
		mac := normalizeMAC(info.MAC)
		id := t.canonicalIDLocked(identity(mac, ip))
		if mac != "" {
			macs[id] = mac
			t.aliases["ip:"+ip] = id
		}

		profile := t.profiles[id]
		profile.ID = id
		if mac != "" {
			profile.MAC = mac
		}
		if addr, err := netip.ParseAddr(ip); err == nil {
			profile.Addresses = uniqueHostAddresses(append(
				profile.Addresses, hostAddress(addr),
			))
		}
		t.profiles[id] = profile
	}
	for mac, info := range t.leaseByMAC {
		hostname := strings.TrimSpace(info.Hostname)
		if hostname == "" {
			continue
		}
		id := t.canonicalIDLocked(identity(mac, info.IP))
		names[id] = hostname
		macs[id] = mac
		profile := t.profiles[id]
		profile.ID = id
		profile.Hostname = hostname
		profile.MAC = mac
		t.profiles[id] = profile
	}

	for ip, host := range t.hosts {
		id := t.identityForIPLocked(ip)
		hostname := names[id]
		if hostname == "" {
			continue
		}
		host.Hostname = hostname
		if mac := macs[id]; mac != "" {
			host.MAC = mac
		}
		t.hosts[ip] = host
	}
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

type addressActivity struct {
	address model.HostAddress
	rank    int
}

func (t *Tracker) currentAddressesLocked(
	id string, mac string,
) []model.HostAddress {
	id = t.canonicalIDLocked(id)
	candidates := make(map[string]addressActivity)
	add := func(ip string, rank int) {
		addr, err := netip.ParseAddr(ip)
		if err != nil {
			return
		}
		addr = addr.Unmap()
		value := hostAddress(addr)
		current := candidates[value.IP]
		if rank > current.rank {
			candidates[value.IP] = addressActivity{address: value, rank: rank}
		}
	}

	// An address with a live conntrack entry is actively in use.
	for ip, host := range t.hosts {
		if host.ActiveFlows > 0 && t.identityForIPLocked(ip) == id {
			add(ip, 3)
		}
	}

	// REACHABLE/DELAY/PROBE entries are stronger evidence than STALE entries.
	// STALE remains useful for displaying an idle device when no stronger
	// address exists for that address family.
	for ip, entry := range t.neighbors {
		addr, err := netip.ParseAddr(ip)
		if err != nil || !entry.Online ||
			!t.neighborIsLANLocked(addr, entry) ||
			t.identityForIPLocked(ip) != id {
			continue
		}
		rank := 1
		if entry.State != "STALE" {
			rank = 2
		}
		add(ip, rank)
	}

	// dnsmasq may temporarily retain the previous lease after a device moves
	// between subnets. Only its preferred current lease is current-address
	// evidence; every lease is still retained in the device profile/history.
	if current, found := t.leaseByMAC[normalizeMAC(mac)]; found {
		add(current.IP, 2)
	}

	strongFamily := make(map[string]bool)
	for _, candidate := range candidates {
		if candidate.rank >= 2 {
			strongFamily[candidate.address.Family] = true
		}
	}
	addresses := make([]model.HostAddress, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.rank >= 2 ||
			!strongFamily[candidate.address.Family] {
			addresses = append(addresses, candidate.address)
		}
	}
	return uniqueHostAddresses(addresses)
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
		if group.Hostname == "" && host.Hostname != "" {
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
		groups[id] = group
	}
	for id, group := range groups {
		info, found := t.leaseByMAC[normalizeMAC(group.MAC)]
		if !found {
			continue
		}
		if info.Hostname != "" {
			group.Hostname = info.Hostname
		}
		if group.MAC == "" {
			group.MAC = normalizeMAC(info.MAC)
		}
		groups[id] = group
	}

	hosts := make([]model.Host, 0, len(groups))
	for _, host := range groups {
		host.Addresses = t.currentAddressesLocked(host.ID, host.MAC)
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
