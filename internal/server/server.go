package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/op-flow-insight/op-flow-insight/internal/collector"
	"github.com/op-flow-insight/op-flow-insight/internal/dataset"
)

type Coordinator struct {
	mu      sync.Mutex
	running bool
	dataDir string
	data    *dataset.Manager
}

func NewCoordinator(dataDir string, data *dataset.Manager) *Coordinator {
	return &Coordinator{dataDir: dataDir, data: data}
}

func (c *Coordinator) Trigger(parent context.Context) bool {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return false
	}
	c.running = true
	c.data.SetUpdateState(true, nil)
	c.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(parent, 20*time.Minute)
		defer cancel()
		err := c.update(ctx)
		c.mu.Lock()
		c.running = false
		c.mu.Unlock()
		c.data.SetUpdateState(false, err)
	}()
	return true
}

func (c *Coordinator) UpdateSync(ctx context.Context) error {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return errors.New("dataset update already running")
	}
	c.running = true
	c.data.SetUpdateState(true, nil)
	c.mu.Unlock()
	err := c.update(ctx)
	c.mu.Lock()
	c.running = false
	c.mu.Unlock()
	c.data.SetUpdateState(false, err)
	return err
}

func (c *Coordinator) update(ctx context.Context) error {
	_, updateErr := dataset.Update(ctx, c.dataDir)
	reloadErr := c.data.Reload()
	return errors.Join(updateErr, reloadErr)
}

type API struct {
	tracker *collector.Tracker
	data    *dataset.Manager
	updates *Coordinator
}

func NewAPI(tracker *collector.Tracker, data *dataset.Manager, updates *Coordinator) *API {
	return &API{tracker: tracker, data: data, updates: updates}
}

func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/dashboard", a.dashboard)
	mux.HandleFunc("/v1/health", a.health)
	mux.HandleFunc("/v1/lookup", a.lookup)
	mux.HandleFunc("/v1/update", a.update)
	mux.HandleFunc("/v1/reset", a.reset)
	mux.HandleFunc("/v1/history", a.history)
	mux.HandleFunc("/v1/export", a.export)
	return securityHeaders(mux)
}

func (a *API) dashboard(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.tracker.Snapshot())
}

func (a *API) health(w http.ResponseWriter, _ *http.Request) {
	snapshot := a.tracker.Snapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"generated_at": snapshot.GeneratedAt,
		"health":       snapshot.Health,
		"data":         snapshot.Data,
	})
}

func (a *API) lookup(w http.ResponseWriter, r *http.Request) {
	addr, err := netip.ParseAddr(r.URL.Query().Get("ip"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid IP address"})
		return
	}
	geo, risk := a.data.Lookup(addr)
	writeJSON(w, http.StatusOK, map[string]any{"ip": addr.String(), "geo": geo, "risk": risk})
}

func (a *API) update(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return
	}
	accepted := a.updates.Trigger(context.Background())
	writeJSON(w, http.StatusAccepted, map[string]bool{"accepted": accepted})
}

func (a *API) reset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Query().Get("confirm") != "true" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "POST with confirm=true required"})
		return
	}
	if err := a.tracker.ResetCounters(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"reset": true})
}

func (a *API) history(w http.ResponseWriter, r *http.Request) {
	history, err := a.tracker.UsageHistory(
		r.URL.Query().Get("granularity"), r.URL.Query().Get("period"),
	)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, history)
}

func (a *API) export(w http.ResponseWriter, r *http.Request) {
	filename, content, err := a.tracker.ExportUsageTXT(
		r.URL.Query().Get("granularity"), r.URL.Query().Get("period"),
	)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"filename": filename,
		"content":  content,
	})
}

func ServeUnix(ctx context.Context, socketPath string, handler http.Handler) error {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return err
	}
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	defer listener.Close()
	if err := os.Chmod(socketPath, 0o600); err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	err = srv.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func Ctl(ctx context.Context, socketPath, action string, args []string, output io.Writer) error {
	path := ""
	method := http.MethodGet
	switch action {
	case "dashboard":
		path = "/v1/dashboard"
	case "health":
		path = "/v1/health"
	case "lookup":
		if len(args) != 1 {
			return errors.New("lookup requires one IP address")
		}
		path = "/v1/lookup?ip=" + url.QueryEscape(args[0])
	case "update":
		path, method = "/v1/update", http.MethodPost
	case "reset":
		path, method = "/v1/reset?confirm=true", http.MethodPost
	case "history", "export":
		if len(args) != 2 {
			return fmt.Errorf("%s requires granularity and period", action)
		}
		path = "/v1/" + action +
			"?granularity=" + url.QueryEscape(args[0]) +
			"&period=" + url.QueryEscape(args[1])
	default:
		return fmt.Errorf("unknown ctl action %q", action)
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socketPath)
		},
	}
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, method, "http://unix"+path, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return fmt.Errorf("API %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	_, err = io.Copy(output, resp.Body)
	return err
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}
