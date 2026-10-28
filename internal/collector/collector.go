package collector

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/op-flow-insight/op-flow-insight/internal/config"
	"github.com/op-flow-insight/op-flow-insight/internal/conntrack"
	"github.com/op-flow-insight/op-flow-insight/internal/dataset"
	"github.com/op-flow-insight/op-flow-insight/internal/model"
	"github.com/op-flow-insight/op-flow-insight/internal/store"
)

type Tracker struct {
	mu                 sync.RWMutex
	saveMu             sync.Mutex
	cachedSnapshot     atomic.Value
	usageCacheMu       sync.RWMutex
	usageCache         map[string]model.UsageHistory
	cfg                config.Config
	data               *dataset.Manager
	hosts              map[string]model.Host
	active             map[string]store.Baseline
	flows              []model.Flow
	history            []model.RatePoint
	daily              map[string]map[string]model.TrafficUsage
	profiles           map[string]model.HostProfile
	aliases            map[string]string
	currentMonth       string
	health             model.Health
	started            time.Time
	lastPoll           time.Time
	lastHistory        time.Time
	leases             map[string]lease
	leaseByMAC         map[string]lease
	hostnamesByMAC     map[string]string
	neighbors          map[string]neighbor
	localAddrs         map[netip.Addr]bool
	lanPrefixes        []netip.Prefix
	lanDevices         map[string]bool
	routerLANAddresses []model.HostAddress
	lastLeaseRead      time.Time
	lastNeighborRead   time.Time
	lastPrefixRead     time.Time
	version            string
	lastSaveError      string
}

type lease struct {
	IP        string
	Hostname  string
	MAC       string
	ExpiresAt int64
}

const leaseRefreshInterval = 2 * time.Second
const minimumBaselineRetention = 30 * time.Second

func New(cfg config.Config, data *dataset.Manager, version string) (*Tracker, error) {
	persisted, err := store.Load(cfg.StateFile)
	if err != nil {
		// A damaged state must not prevent monitoring. Preserve the warning and
		// begin from a clean state.
		persisted = store.Empty()
	}
	tracker := &Tracker{
		cfg:          cfg,
		data:         data,
		hosts:        persisted.Hosts,
		active:       persisted.Active,
		history:      persisted.History,
		daily:        persisted.Daily,
		profiles:     persisted.Profiles,
		aliases:      persisted.Aliases,
		currentMonth: persisted.CurrentMonth,
		health: model.Health{
			ConntrackReadable: false,
			AccountingEnabled: false,
		},
		started:        time.Now().UTC(),
		leases:         make(map[string]lease),
		leaseByMAC:     make(map[string]lease),
		hostnamesByMAC: make(map[string]string),
		neighbors:      make(map[string]neighbor),
		localAddrs:     localInterfaceAddrs(),
		lanPrefixes:    append([]netip.Prefix(nil), cfg.LANPrefixes...),
		lanDevices:     make(map[string]bool),
		usageCache:     make(map[string]model.UsageHistory),
		version:        version,
	}
	tracker.ensureMonth(time.Now())
	tracker.primeUsageCache()
	if err != nil {
		tracker.health.Warnings = append(tracker.health.Warnings,
			"Cumulative state file is damaged; restarted from current connections: "+err.Error())
	}
	// Keep a complete initial response available before the first collector
	// poll. Snapshot() will refresh this cache whenever it can acquire the
	// tracker read lock without waiting.
	tracker.Snapshot()
	return tracker, nil
}

func (t *Tracker) Run(ctx context.Context) {
	saveTicker := time.NewTicker(t.cfg.SaveInterval)
	defer saveTicker.Stop()
	t.pollFile(time.Now().UTC())
	// Schedule the next collection after the previous one completes. With a
	// fixed ticker, a slow scan leaves a tick queued and immediately starts the
	// next write-locked scan, starving dashboard/history readers indefinitely.
	pollTimer := time.NewTimer(t.cfg.PollInterval)
	defer pollTimer.Stop()
	for {
		select {
		case <-pollTimer.C:
			t.pollFile(time.Now().UTC())
			pollTimer.Reset(t.cfg.PollInterval)
		case <-saveTicker.C:
			_ = t.Save()
		case <-ctx.Done():
			_ = t.Save()
			return
		}
	}
}

func (t *Tracker) pollFile(now time.Time) {
	t.refreshLANPrefixes(now)
	t.refreshNeighbors(now)
	f, err := os.Open(t.cfg.ConntrackPath)
	if err != nil {
		t.mu.Lock()
		t.health.ConntrackReadable = false
		t.health.Warnings = uniqueWarnings(append(t.health.Warnings,
			"Unable to read conntrack: "+err.Error(),
		))
		t.mu.Unlock()
		return
	}
	defer f.Close()
	t.Poll(f, now)
}

// Poll is exported to make the accounting logic testable with captured,
// non-sensitive conntrack fixtures.
func (t *Tracker) Poll(r io.Reader, now time.Time) {
	parsed := conntrack.Parse(r)
	t.mu.Lock()

	t.health.ConntrackReadable = true
	t.health.AccountingEnabled = parsed.HasBytes
	t.health.Warnings = removeWarningPrefixes(t.health.Warnings,
		"Unable to read conntrack", "Conntrack byte counters are unavailable")
	if parsed.Lines > 0 && !parsed.HasBytes {
		t.health.Warnings = append(t.health.Warnings,
			"Conntrack byte counters are unavailable; enable net.netfilter.nf_conntrack_acct=1 and establish new connections",
		)
	}

	initialPoll := t.lastPoll.IsZero()
	elapsed := t.cfg.PollInterval.Seconds()
	if !initialPoll && now.After(t.lastPoll) {
		elapsed = now.Sub(t.lastPoll).Seconds()
	}
	if elapsed <= 0 {
		elapsed = 1
	}
	t.lastPoll = now
	t.ensureMonthLocked(now)

	if t.lastLeaseRead.IsZero() ||
		now.Sub(t.lastLeaseRead) >= leaseRefreshInterval {
		t.refreshLeasesLocked(now)
	}

	for ip, host := range t.hosts {
		host.UploadBPS = 0
		host.DownloadBPS = 0
		host.ActiveFlows = 0
		host.MaxRisk = 0
		t.hosts[ip] = host
	}

	observed := make(map[string]bool, len(parsed.Connections))
	flows := make([]model.Flow, 0, len(parsed.Connections))
	var totalUpRate, totalDownRate float64
	for _, conn := range parsed.Connections {
		classified, ok := t.classify(conn)
		if !ok {
			continue
		}
		key, previous, seen := t.connectionBaselineLocked(conn)
		upDelta, downDelta := uint64(0), uint64(0)
		if seen || !initialPoll {
			upDelta = counterDelta(classified.uploaded, previous.Uploaded, seen)
			downDelta = counterDelta(classified.downloaded, previous.Download, seen)
		}
		upRate := float64(upDelta) / elapsed
		downRate := float64(downDelta) / elapsed

		host := t.hosts[classified.host.String()]
		host.IP = classified.host.String()
		hostID, hostname, mac := t.resolveIdentityLocked(host.IP)
		if hostname != "" {
			host.Hostname = hostname
		}
		if mac != "" {
			host.MAC = mac
		}
		host.Uploaded += upDelta
		host.Downloaded += downDelta
		host.UploadBPS += upRate
		host.DownloadBPS += downRate
		host.ActiveFlows++
		host.LastSeen = now

		geo, risk := t.data.Lookup(classified.remote)
		if risk.Score > host.MaxRisk {
			host.MaxRisk = risk.Score
		}
		t.hosts[host.IP] = host
		t.rememberProfileLocked(hostID, host, classified.host, now)
		t.recordUsageLocked(now, hostID, upDelta, downDelta)
		totalUpRate += upRate
		totalDownRate += downRate

		flow := model.Flow{
			ID:          conn.Key(),
			HostID:      hostID,
			HostIP:      host.IP,
			IPVersion:   addressFamily(classified.host),
			Protocol:    conn.Protocol,
			Direction:   classified.direction,
			Source:      classified.source,
			Destination: classified.destination,
			RemoteIP:    classified.remote.String(),
			Uploaded:    classified.uploaded,
			Downloaded:  classified.downloaded,
			UploadBPS:   upRate,
			DownloadBPS: downRate,
			Geo:         geo,
			Risk:        risk,
		}
		flows = append(flows, flow)
		t.active[key] = store.Baseline{
			HostIP: host.IP, Uploaded: classified.uploaded,
			Download: classified.downloaded, LastSeen: now,
		}
		observed[key] = true
	}
	sort.Slice(flows, func(i, j int) bool {
		left := flows[i].UploadBPS + flows[i].DownloadBPS
		right := flows[j].UploadBPS + flows[j].DownloadBPS
		if left == right {
			return flows[i].Risk.Score > flows[j].Risk.Score
		}
		return left > right
	})
	if len(flows) > t.cfg.MaxFlows {
		flows = flows[:t.cfg.MaxFlows]
	}
	// Keep recently absent entries long enough for a delayed ctnetlink destroy
	// event to reconcile against the last sampled counters. Removing a baseline
	// immediately caused the destroy event to import the connection's complete
	// byte counters for a second time.
	cutoff := now.Add(-t.baselineRetention())
	for key, baseline := range t.active {
		if !observed[key] && baseline.LastSeen.Before(cutoff) {
			delete(t.active, key)
		}
	}
	t.flows = flows
	refreshUsageCache := false
	if t.lastHistory.IsZero() || now.Sub(t.lastHistory) >= 5*time.Second {
		t.history = append(t.history, model.RatePoint{
			At: now, UploadBPS: totalUpRate, DownloadBPS: totalDownRate,
		})
		if len(t.history) > 120 {
			t.history = append([]model.RatePoint(nil), t.history[len(t.history)-120:]...)
		}
		t.lastHistory = now
		refreshUsageCache = true
	}
	t.mu.Unlock()
	if refreshUsageCache {
		t.primeUsageCache()
	}
}

// ApplyDestroy reconciles the final counters emitted by ctnetlink. Final
// baselines are retained for a grace period so delayed or duplicate events and
// stale /proc snapshots cannot count the same connection twice.
func (t *Tracker) ApplyDestroy(conn conntrack.Connection) {
	if !conn.HasBytes {
		return
	}
	now := time.Now().UTC()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ensureMonthLocked(now)
	classified, ok := t.classify(conn)
	if !ok {
		return
	}
	key, previous, seen := t.connectionBaselineLocked(conn)
	upDelta := counterDelta(classified.uploaded, previous.Uploaded, seen)
	downDelta := counterDelta(classified.downloaded, previous.Download, seen)
	host := t.hosts[classified.host.String()]
	host.IP = classified.host.String()
	hostID, hostname, mac := t.resolveIdentityLocked(host.IP)
	if hostname != "" {
		host.Hostname = hostname
	}
	if mac != "" {
		host.MAC = mac
	}
	host.Uploaded += upDelta
	host.Downloaded += downDelta
	host.LastSeen = now
	t.hosts[host.IP] = host
	t.rememberProfileLocked(hostID, host, classified.host, now)
	t.recordUsageLocked(now, hostID, upDelta, downDelta)
	t.active[key] = store.Baseline{
		HostIP: host.IP, Uploaded: classified.uploaded,
		Download: classified.downloaded, LastSeen: now,
	}
}

func (t *Tracker) connectionBaselineLocked(
	conn conntrack.Connection,
) (string, store.Baseline, bool) {
	key := conn.Key()
	if baseline, found := t.active[key]; found {
		return key, baseline, true
	}
	legacyKey := conn.LegacyKey()
	if baseline, found := t.active[legacyKey]; found {
		delete(t.active, legacyKey)
		return key, baseline, true
	}
	return key, store.Baseline{}, false
}

func (t *Tracker) baselineRetention() time.Duration {
	retention := 3 * t.cfg.PollInterval
	if retention < minimumBaselineRetention {
		return minimumBaselineRetention
	}
	return retention
}

func (t *Tracker) SetDestroyEventHealth(active bool, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.health.DestroyEvents = active
	t.health.Warnings = removeWarningPrefixes(t.health.Warnings, "Conntrack destroy events")
	if err != nil {
		t.health.Warnings = append(t.health.Warnings,
			"Conntrack destroy events are unavailable; very short connections may be undercounted: "+err.Error(),
		)
	}
}

type classified struct {
	host        netip.Addr
	remote      netip.Addr
	direction   string
	source      model.Endpoint
	destination model.Endpoint
	uploaded    uint64
	downloaded  uint64
}

func (t *Tracker) classify(conn conntrack.Connection) (classified, bool) {
	origSrcLAN := t.isLAN(conn.Original.Source)
	origDstLAN := t.isLAN(conn.Original.Destination)
	replySrcLAN := t.isLAN(conn.Reply.Source)
	if origSrcLAN && !origDstLAN {
		if t.localAddrs[conn.Original.Source] {
			return classified{}, false
		}
		return classified{
			host: conn.Original.Source, remote: conn.Original.Destination,
			direction:   "outbound",
			source:      model.Endpoint{IP: conn.Original.Source.String(), Port: conn.Original.SourcePort},
			destination: model.Endpoint{IP: conn.Original.Destination.String(), Port: conn.Original.DestPort},
			uploaded:    conn.Original.Bytes, downloaded: conn.Reply.Bytes,
		}, true
	}
	if !origSrcLAN && origDstLAN {
		if t.localAddrs[conn.Original.Destination] {
			return classified{}, false
		}
		return classified{
			host: conn.Original.Destination, remote: conn.Original.Source,
			direction:   "inbound",
			source:      model.Endpoint{IP: conn.Original.Source.String(), Port: conn.Original.SourcePort},
			destination: model.Endpoint{IP: conn.Original.Destination.String(), Port: conn.Original.DestPort},
			uploaded:    conn.Reply.Bytes, downloaded: conn.Original.Bytes,
		}, true
	}
	// DNAT commonly exposes the router's WAN address in the original tuple and
	// the real LAN target as the reply source.
	if !origSrcLAN && replySrcLAN {
		if t.localAddrs[conn.Reply.Source] {
			return classified{}, false
		}
		return classified{
			host: conn.Reply.Source, remote: conn.Original.Source,
			direction:   "inbound",
			source:      model.Endpoint{IP: conn.Original.Source.String(), Port: conn.Original.SourcePort},
			destination: model.Endpoint{IP: conn.Reply.Source.String(), Port: conn.Reply.SourcePort},
			uploaded:    conn.Reply.Bytes, downloaded: conn.Original.Bytes,
		}, true
	}
	return classified{}, false
}

func localInterfaceAddrs() map[netip.Addr]bool {
	out := make(map[netip.Addr]bool)
	interfaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, raw := range addrs {
			value := raw.String()
			if i := strings.LastIndexByte(value, '/'); i >= 0 {
				value = value[:i]
			}
			if addr, err := netip.ParseAddr(value); err == nil {
				out[addr] = true
			}
		}
	}
	return out
}

func (t *Tracker) isLAN(addr netip.Addr) bool {
	for _, prefix := range t.lanPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func counterDelta(current, previous uint64, seen bool) uint64 {
	if !seen {
		return current
	}
	if current >= previous {
		return current - previous
	}
	// The tuple was reused or conntrack reset its counter.
	return current
}

func (t *Tracker) Snapshot() model.Dashboard {
	// Poll() deliberately updates a complete interval under the write lock so
	// hosts, totals and flows stay mutually consistent. A large conntrack table
	// can make that interval noticeably longer, though. Blocking an rpcd helper
	// behind the writer also blocks LuCI XHRs and can eventually starve LuCI
	// authentication. Return the most recent complete snapshot while a poll is
	// in progress instead of waiting on the collector lock.
	if t.mu.TryRLock() {
		snapshot := t.snapshotLocked()
		t.mu.RUnlock()
		t.cachedSnapshot.Store(snapshot)
		return snapshot
	}
	if cached := t.cachedSnapshot.Load(); cached != nil {
		return cached.(model.Dashboard)
	}
	// New() primes the cache, so this is only a defensive fallback for a
	// zero-value Tracker used outside the normal construction path.
	return model.Dashboard{GeneratedAt: time.Now().UTC()}
}

func (t *Tracker) snapshotLocked() model.Dashboard {
	hosts := t.onlineHostsLocked()
	var totals model.Totals
	for _, host := range t.hosts {
		totals.Uploaded += host.Uploaded
		totals.Downloaded += host.Downloaded
		totals.UploadBPS += host.UploadBPS
		totals.DownloadBPS += host.DownloadBPS
		if host.MaxRisk > totals.HighestRisk {
			totals.HighestRisk = host.MaxRisk
		}
	}
	totals.ActiveHosts = len(hosts)
	flows := t.snapshotFlowsLocked()
	totals.ActiveFlows = len(flows)
	totals.Period = t.currentMonth
	totals.ResetAt, totals.NextResetAt = monthBounds(t.currentMonth)
	health := t.health
	health.Warnings = append([]string(nil), t.health.Warnings...)
	health.LANPrefixes = make([]string, 0, len(t.lanPrefixes))
	for _, prefix := range t.lanPrefixes {
		health.LANPrefixes = append(health.LANPrefixes, prefix.String())
	}
	health.RouterLANAddresses = append(
		[]model.HostAddress(nil), t.routerLANAddresses...,
	)
	if t.lastSaveError != "" {
		health.Warnings = append(health.Warnings, "Failed to save cumulative state: "+t.lastSaveError)
	}
	return model.Dashboard{
		Version: t.version, GeneratedAt: time.Now().UTC(),
		UptimeSec: int64(time.Since(t.started).Seconds()),
		Totals:    totals, Hosts: hosts,
		Flows:        flows,
		History:      append([]model.RatePoint(nil), t.history...),
		UsagePeriods: t.usagePeriodsLocked(),
		Data:         t.data.Status(), Health: health,
	}
}

func (t *Tracker) Save() error {
	t.saveMu.Lock()
	defer t.saveMu.Unlock()
	t.mu.RLock()
	state := store.State{
		Version:      store.CurrentVersion,
		Hosts:        make(map[string]model.Host, len(t.hosts)),
		Active:       make(map[string]store.Baseline, len(t.active)),
		History:      append([]model.RatePoint(nil), t.history...),
		Daily:        cloneDaily(t.daily),
		Profiles:     cloneProfiles(t.profiles),
		Aliases:      cloneStrings(t.aliases),
		CurrentMonth: t.currentMonth,
	}
	for key, value := range t.hosts {
		state.Hosts[key] = value
	}
	for key, value := range t.active {
		state.Active[key] = value
	}
	t.mu.RUnlock()
	err := store.Save(t.cfg.StateFile, state)
	t.mu.Lock()
	if err != nil {
		t.lastSaveError = err.Error()
	} else {
		t.lastSaveError = ""
	}
	t.mu.Unlock()
	return err
}

func (t *Tracker) ResetCounters() error {
	now := time.Now().UTC()
	t.mu.Lock()
	t.ensureMonthLocked(now)
	for key, host := range t.hosts {
		host.Uploaded = 0
		host.Downloaded = 0
		host.CounterResetAt = now
		t.hosts[key] = host
	}
	// A manual reset is also the administrator's recovery path for counters
	// accumulated by an older buggy release. Remove only the current local
	// month's retained daily buckets so overview totals, history and exports all
	// restart from the same clean point; previous months remain untouched.
	for day := range t.daily {
		if strings.HasPrefix(day, t.currentMonth+"-") {
			delete(t.daily, day)
		}
	}
	t.history = nil
	t.mu.Unlock()
	t.clearUsageCache()
	t.primeUsageCache()
	return t.Save()
}

func readLeases(path string) map[string]lease {
	out := make(map[string]lease)
	raw, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	now := time.Now().Unix()
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		expiresAt, _ := strconv.ParseInt(fields[0], 10, 64)
		if expiresAt > 0 && expiresAt <= now {
			continue
		}
		ip := fields[2]
		if _, err := netip.ParseAddr(ip); err != nil {
			continue
		}
		hostname := fields[3]
		if hostname == "*" {
			hostname = ""
		}
		out[ip] = lease{
			IP: ip, Hostname: hostname, MAC: strings.ToUpper(fields[1]),
			ExpiresAt: expiresAt,
		}
	}
	return out
}

func removeWarningPrefixes(items []string, prefixes ...string) []string {
	out := items[:0]
	for _, item := range items {
		remove := false
		for _, prefix := range prefixes {
			if strings.HasPrefix(item, prefix) {
				remove = true
				break
			}
		}
		if !remove {
			out = append(out, item)
		}
	}
	return out
}

func uniqueWarnings(items []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(items))
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	return out
}

func (t *Tracker) DebugSummary() string {
	snapshot := t.Snapshot()
	return fmt.Sprintf("hosts=%d flows=%d up=%d down=%d",
		len(snapshot.Hosts), len(snapshot.Flows),
		snapshot.Totals.Uploaded, snapshot.Totals.Downloaded,
	)
}
