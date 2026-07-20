package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/op-flow-insight/op-flow-insight/internal/model"
)

func TestLoadMigratesV1WithoutDroppingTotals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	savedAt := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	v1 := State{
		Version: 1,
		SavedAt: savedAt,
		Hosts: map[string]model.Host{
			"192.168.1.23": {
				IP: "192.168.1.23", Hostname: "dual-stack-host",
				MAC: "02:11:22:33:44:55", Downloaded: 2000, Uploaded: 1000,
			},
			"fd00::23": {
				IP: "fd00::23", Hostname: "dual-stack-host",
				MAC: "02:11:22:33:44:55", Downloaded: 4000, Uploaded: 3000,
			},
		},
		Active: map[string]Baseline{
			"flow": {HostIP: "192.168.1.23", Download: 2000, Uploaded: 1000},
		},
	}
	raw, err := json.Marshal(v1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != CurrentVersion {
		t.Fatalf("version = %d, want %d", got.Version, CurrentVersion)
	}
	if got.Hosts["192.168.1.23"].Downloaded != 2000 ||
		got.Active["flow"].Download != 2000 {
		t.Fatalf("legacy counters were not retained: %+v", got)
	}
	day := savedAt.In(time.Local).Format("2006-01-02")
	id := "mac:02:11:22:33:44:55"
	if got.Daily[day][id].Downloaded != 6000 ||
		got.Daily[day][id].Uploaded != 4000 {
		t.Fatalf("legacy totals were not archived: %+v", got.Daily)
	}
	if got.Profiles[id].Hostname != "dual-stack-host" ||
		got.Aliases["ip:192.168.1.23"] != id {
		t.Fatalf("legacy identity was not migrated: profiles=%+v aliases=%+v",
			got.Profiles, got.Aliases)
	}
	if len(got.Profiles[id].Addresses) != 2 ||
		got.Profiles[id].Addresses[1].Scope != "lan" {
		t.Fatalf("legacy dual-stack addresses were not merged: %+v",
			got.Profiles[id].Addresses)
	}
}
