package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/op-flow-insight/op-flow-insight/internal/collector"
	"github.com/op-flow-insight/op-flow-insight/internal/config"
	"github.com/op-flow-insight/op-flow-insight/internal/dataset"
	"github.com/op-flow-insight/op-flow-insight/internal/model"
)

func TestDashboardHostFilterKeepsOverviewLightweight(t *testing.T) {
	cfg := config.Default()
	cfg.StateFile = filepath.Join(t.TempDir(), "state.json")
	cfg.LANPrefixes = []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}
	tracker, err := collector.New(cfg, dataset.NewManager(t.TempDir()), "test")
	if err != nil {
		t.Fatal(err)
	}
	fixture := strings.Join([]string{
		"ipv4 2 tcp 6 100 ESTABLISHED src=192.168.1.20 dst=1.1.1.1 sport=50000 dport=443 packets=1 bytes=100 src=1.1.1.1 dst=192.168.1.20 sport=443 dport=50000 packets=2 bytes=1000 [ASSURED]",
		"ipv4 2 tcp 6 100 ESTABLISHED src=192.168.1.30 dst=8.8.8.8 sport=50001 dport=443 packets=1 bytes=100 src=8.8.8.8 dst=192.168.1.30 sport=443 dport=50001 packets=2 bytes=1000 [ASSURED]",
	}, "\n") + "\n"
	tracker.Poll(strings.NewReader(fixture), time.Now().UTC())
	api := &API{tracker: tracker}

	full := requestDashboard(t, api, "/v1/dashboard")
	if len(full.Flows) != 2 || full.Totals.ActiveFlows != 2 {
		t.Fatalf("full dashboard flows=%d totals=%d, want 2/2",
			len(full.Flows), full.Totals.ActiveFlows)
	}

	compact := requestDashboard(t, api, "/v1/dashboard?host_id=")
	if len(compact.Flows) != 0 || compact.Totals.ActiveFlows != 2 {
		t.Fatalf("compact dashboard flows=%d totals=%d, want 0/2",
			len(compact.Flows), compact.Totals.ActiveFlows)
	}

	hostID := full.Flows[0].HostID
	filtered := requestDashboard(t, api,
		"/v1/dashboard?host_id="+url.QueryEscape(hostID))
	if len(filtered.Flows) != 1 || filtered.Flows[0].HostID != hostID {
		t.Fatalf("filtered dashboard = %+v, want only host %q",
			filtered.Flows, hostID)
	}
}

func TestDashboardHostFilterLimitsLargeConnectionDetails(t *testing.T) {
	cfg := config.Default()
	cfg.StateFile = filepath.Join(t.TempDir(), "state.json")
	cfg.MaxFlows = maxDashboardFlowDetails + 100
	cfg.LANPrefixes = []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}
	tracker, err := collector.New(cfg, dataset.NewManager(t.TempDir()), "test")
	if err != nil {
		t.Fatal(err)
	}
	lines := make([]string, 0, maxDashboardFlowDetails+44)
	for index := 0; index < maxDashboardFlowDetails+44; index++ {
		lines = append(lines, fmt.Sprintf(
			"ipv4 2 tcp 6 100 ESTABLISHED src=192.168.1.20 dst=1.1.1.1 sport=%d dport=443 packets=1 bytes=100 src=1.1.1.1 dst=192.168.1.20 sport=443 dport=%d packets=2 bytes=1000 [ASSURED]",
			10000+index, 10000+index,
		))
	}
	tracker.Poll(strings.NewReader(strings.Join(lines, "\n")+"\n"), time.Now().UTC())
	api := &API{tracker: tracker}
	full := requestDashboard(t, api, "/v1/dashboard")
	if len(full.Flows) == 0 {
		t.Fatal("full dashboard has no flows")
	}
	hostID := full.Flows[0].HostID
	filtered := requestDashboard(t, api,
		"/v1/dashboard?host_id="+url.QueryEscape(hostID))
	if len(filtered.Flows) != maxDashboardFlowDetails {
		t.Fatalf("filtered flow count = %d, want %d",
			len(filtered.Flows), maxDashboardFlowDetails)
	}
	if filtered.FlowTotal != maxDashboardFlowDetails+44 || !filtered.FlowsLimited {
		t.Fatalf("flow metadata = total %d limited %v, want %d/true",
			filtered.FlowTotal, filtered.FlowsLimited, maxDashboardFlowDetails+44)
	}
}

func requestDashboard(t *testing.T, api *API, target string) model.Dashboard {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", target, nil)
	api.dashboard(recorder, request)
	if recorder.Code != 200 {
		t.Fatalf("dashboard status = %d", recorder.Code)
	}
	var result model.Dashboard
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
