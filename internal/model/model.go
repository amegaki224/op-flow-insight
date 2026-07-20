package model

import "time"

// Geo describes coarse, offline IP ownership/location information.
type Geo struct {
	CountryCode string `json:"country_code,omitempty"`
	ASN         uint32 `json:"asn,omitempty"`
	ASNOrg      string `json:"asn_org,omitempty"`
}

type Evidence struct {
	Source   string `json:"source"`
	Category string `json:"category"`
	Severity int    `json:"severity"`
	Detail   string `json:"detail,omitempty"`
}

// Risk is an evidence score. A zero score means "not observed in loaded
// datasets", not "known safe".
type Risk struct {
	Score    int        `json:"score"`
	Level    string     `json:"level"`
	Evidence []Evidence `json:"evidence,omitempty"`
}

type HostAddress struct {
	IP     string `json:"ip"`
	Family string `json:"family"`
	Scope  string `json:"scope,omitempty"`
}

type Host struct {
	ID             string        `json:"id,omitempty"`
	IP             string        `json:"ip"`
	Hostname       string        `json:"hostname,omitempty"`
	MAC            string        `json:"mac,omitempty"`
	Addresses      []HostAddress `json:"addresses,omitempty"`
	Online         bool          `json:"online"`
	Uploaded       uint64        `json:"uploaded"`
	Downloaded     uint64        `json:"downloaded"`
	UploadBPS      float64       `json:"upload_bps"`
	DownloadBPS    float64       `json:"download_bps"`
	ActiveFlows    int           `json:"active_flows"`
	MaxRisk        int           `json:"max_risk"`
	LastSeen       time.Time     `json:"last_seen"`
	CounterResetAt time.Time     `json:"counter_reset_at,omitempty"`
}

type Endpoint struct {
	IP   string `json:"ip"`
	Port uint16 `json:"port,omitempty"`
}

type Flow struct {
	ID          string   `json:"id"`
	HostID      string   `json:"host_id,omitempty"`
	HostIP      string   `json:"host_ip"`
	IPVersion   string   `json:"ip_version,omitempty"`
	Protocol    string   `json:"protocol"`
	Direction   string   `json:"direction"`
	Source      Endpoint `json:"source"`
	Destination Endpoint `json:"destination"`
	RemoteIP    string   `json:"remote_ip"`
	Uploaded    uint64   `json:"uploaded"`
	Downloaded  uint64   `json:"downloaded"`
	UploadBPS   float64  `json:"upload_bps"`
	DownloadBPS float64  `json:"download_bps"`
	Geo         Geo      `json:"geo"`
	Risk        Risk     `json:"risk"`
}

type RatePoint struct {
	At          time.Time `json:"at"`
	UploadBPS   float64   `json:"upload_bps"`
	DownloadBPS float64   `json:"download_bps"`
}

type Totals struct {
	Uploaded    uint64    `json:"uploaded"`
	Downloaded  uint64    `json:"downloaded"`
	UploadBPS   float64   `json:"upload_bps"`
	DownloadBPS float64   `json:"download_bps"`
	ActiveHosts int       `json:"active_hosts"`
	ActiveFlows int       `json:"active_flows"`
	HighestRisk int       `json:"highest_risk"`
	Period      string    `json:"period,omitempty"`
	ResetAt     time.Time `json:"reset_at,omitempty"`
	NextResetAt time.Time `json:"next_reset_at,omitempty"`
}

type TrafficUsage struct {
	Uploaded   uint64 `json:"uploaded"`
	Downloaded uint64 `json:"downloaded"`
}

type HostProfile struct {
	ID        string        `json:"id"`
	Hostname  string        `json:"hostname,omitempty"`
	MAC       string        `json:"mac,omitempty"`
	Addresses []HostAddress `json:"addresses,omitempty"`
	LastSeen  time.Time     `json:"last_seen,omitempty"`
}

type UsageRecord struct {
	HostID     string        `json:"host_id"`
	Hostname   string        `json:"hostname,omitempty"`
	MAC        string        `json:"mac,omitempty"`
	Addresses  []HostAddress `json:"addresses,omitempty"`
	Uploaded   uint64        `json:"uploaded"`
	Downloaded uint64        `json:"downloaded"`
}

type UsageHistory struct {
	Granularity string        `json:"granularity"`
	Period      string        `json:"period"`
	Options     []string      `json:"options"`
	Records     []UsageRecord `json:"records"`
	Totals      TrafficUsage  `json:"totals"`
	GeneratedAt time.Time     `json:"generated_at"`
}

type DataStatus struct {
	Loaded          bool              `json:"loaded"`
	UpdatedAt       time.Time         `json:"updated_at,omitempty"`
	UpdateRunning   bool              `json:"update_running"`
	LastUpdateError string            `json:"last_update_error,omitempty"`
	Sources         map[string]string `json:"sources,omitempty"`
	Records         map[string]int    `json:"records,omitempty"`
}

type Health struct {
	ConntrackReadable  bool          `json:"conntrack_readable"`
	AccountingEnabled  bool          `json:"accounting_enabled"`
	DestroyEvents      bool          `json:"destroy_events"`
	LANPrefixes        []string      `json:"lan_prefixes,omitempty"`
	RouterLANAddresses []HostAddress `json:"router_lan_addresses,omitempty"`
	Warnings           []string      `json:"warnings,omitempty"`
}

type Dashboard struct {
	Version      string              `json:"version"`
	GeneratedAt  time.Time           `json:"generated_at"`
	UptimeSec    int64               `json:"uptime_sec"`
	Totals       Totals              `json:"totals"`
	Hosts        []Host              `json:"hosts"`
	Flows        []Flow              `json:"flows"`
	History      []RatePoint         `json:"history"`
	UsagePeriods map[string][]string `json:"usage_periods,omitempty"`
	Data         DataStatus          `json:"data"`
	Health       Health              `json:"health"`
}
