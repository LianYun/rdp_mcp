// Package adminapi implements the management API (docs/admin-api.md) used by the desktop UI and CLI.
package adminapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LianYun/rdp_mcp/internal/audit"
	"github.com/LianYun/rdp_mcp/internal/config"
	"github.com/LianYun/rdp_mcp/internal/events"
	"github.com/LianYun/rdp_mcp/internal/session"
)

type ServiceInfo struct {
	Version    string    `json:"version"`
	PID        int       `json:"pid"`
	Listen     string    `json:"listen"`
	MCPURL     string    `json:"mcp_url"`
	ConfigPath string    `json:"config_path"`
	DataDir    string    `json:"data_dir"`
	ManagedBy  string    `json:"managed_by"`
	StartedAt  time.Time `json:"started_at"`
	WorkerPath string    `json:"worker_path"`
}

type API struct {
	Mgr        *session.Manager
	Bus        *events.Bus
	Audit      *audit.Logger
	Info       ServiceInfo
	AdminToken string
	MCPToken   func() string
	Origins    []string
	Shutdown   func()
	// Keychain stores machine passwords (internal/secrets in production).
	Keychain SecretStore
	// Apply activates a config the API just wrote (manager + token + log level).
	Apply func(*config.Config)
	// LogFile is the service log shown by GET /logs.
	LogFile string

	cfgMu sync.Mutex // serializes config writes
}

func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", a.health)
	mux.Handle("GET /api/v1/service", a.auth(a.service))
	mux.Handle("POST /api/v1/service/shutdown", a.auth(a.shutdown))
	mux.Handle("GET /api/v1/service/mcp-token", a.auth(a.mcpToken))
	mux.Handle("POST /api/v1/service/mcp-token/rotate", a.auth(a.rotateMCPToken))
	mux.Handle("GET /api/v1/machines", a.auth(a.listMachines))
	mux.Handle("POST /api/v1/machines", a.auth(a.createMachine))
	mux.Handle("GET /api/v1/machines/{name}", a.auth(a.getMachine))
	mux.Handle("PUT /api/v1/machines/{name}", a.auth(a.updateMachine))
	mux.Handle("DELETE /api/v1/machines/{name}", a.auth(a.deleteMachine))
	mux.Handle("PUT /api/v1/machines/{name}/secret", a.auth(a.putSecret))
	mux.Handle("POST /api/v1/machines/{name}/test", a.auth(a.testMachine))
	mux.Handle("POST /api/v1/machines/{name}/cert", a.auth(a.answerCert))
	mux.Handle("GET /api/v1/machines/{name}/stream", a.auth(a.stream))
	mux.Handle("GET /api/v1/config", a.auth(a.getConfig))
	mux.Handle("PUT /api/v1/config/server", a.auth(a.putSection("server")))
	mux.Handle("PUT /api/v1/config/defaults", a.auth(a.putSection("defaults")))
	mux.Handle("GET /api/v1/config/raw", a.auth(a.getConfigRaw))
	mux.Handle("PUT /api/v1/config/raw", a.auth(a.putConfigRaw))
	mux.Handle("GET /api/v1/logs", a.auth(a.logs))
	mux.Handle("GET /api/v1/audit/thumbs/{file}", a.auth(a.auditThumb))
	mux.Handle("POST /api/v1/machines/{name}/connect", a.auth(a.connect))
	mux.Handle("POST /api/v1/machines/{name}/disconnect", a.auth(a.disconnect))
	mux.Handle("POST /api/v1/machines/{name}/reconnect", a.auth(a.reconnect))
	mux.Handle("PUT /api/v1/machines/{name}/control", a.auth(a.control))
	mux.Handle("POST /api/v1/machines/{name}/trust", a.auth(a.trust))
	mux.Handle("GET /api/v1/machines/{name}/screenshot", a.auth(a.screenshot))
	mux.Handle("POST /api/v1/control/emergency-stop", a.auth(a.emergencyStop))
	mux.Handle("GET /api/v1/events", a.auth(a.events))
	mux.Handle("GET /api/v1/audit", a.auth(a.audit))
	return a.cors(mux)
}

func (a *API) cors(next http.Handler) http.Handler {
	allowed := append([]string{"tauri://localhost", "http://tauri.localhost", "https://tauri.localhost"}, a.Origins...)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" && (slices.Contains(allowed, o) || isLocalDevOrigin(o)) {
			w.Header().Set("Access-Control-Allow-Origin", o)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isLocalDevOrigin allows the Vite dev server used by `tauri dev`.
func isLocalDevOrigin(o string) bool {
	return strings.HasPrefix(o, "http://localhost:") || strings.HasPrefix(o, "http://127.0.0.1:")
}

func (a *API) auth(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if tok == "" {
			tok = r.URL.Query().Get("token")
		}
		if subtle.ConstantTimeCompare([]byte(tok), []byte(a.AdminToken)) != 1 {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "invalid or missing admin token")
			return
		}
		h(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"status": "ok", "version": a.Info.Version,
		"uptime_s": int(time.Since(a.Info.StartedAt).Seconds())})
}

func (a *API) service(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, a.Info) }

func (a *API) shutdown(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 202, map[string]string{"status": "shutting down"})
	go a.Shutdown()
}

func (a *API) mcpToken(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]string{"mcp_token": a.MCPToken()})
}

func (a *API) session(w http.ResponseWriter, r *http.Request) *session.Session {
	s, err := a.Mgr.Lookup(r.PathValue("name"))
	if err != nil {
		writeErr(w, 404, "not_found", err.Error())
		return nil
	}
	return s
}

func (a *API) listMachines(w http.ResponseWriter, r *http.Request) {
	out := []session.Info{}
	for _, s := range a.Mgr.List() {
		out = append(out, s.Info())
	}
	writeJSON(w, 200, out)
}

func (a *API) getMachine(w http.ResponseWriter, r *http.Request) {
	if s := a.session(w, r); s != nil {
		writeJSON(w, 200, s.Info())
	}
}

func (a *API) connect(w http.ResponseWriter, r *http.Request) {
	s := a.session(w, r)
	if s == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	if err := s.Connect(ctx); err != nil {
		writeErr(w, 502, "connect_failed", err.Error())
		return
	}
	writeJSON(w, 200, s.Info())
}

func (a *API) disconnect(w http.ResponseWriter, r *http.Request) {
	if s := a.session(w, r); s != nil {
		s.Disconnect()
		writeJSON(w, 200, s.Info())
	}
}

func (a *API) reconnect(w http.ResponseWriter, r *http.Request) {
	s := a.session(w, r)
	if s == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	if err := s.Reconnect(ctx); err != nil {
		writeErr(w, 502, "connect_failed", err.Error())
		return
	}
	writeJSON(w, 200, s.Info())
}

func (a *API) control(w http.ResponseWriter, r *http.Request) {
	s := a.session(w, r)
	if s == nil {
		return
	}
	var body struct {
		Mode session.ControlMode `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad_request", err.Error())
		return
	}
	switch body.Mode {
	case session.ModeAgent, session.ModeHuman, session.ModePaused:
	default:
		writeErr(w, 400, "bad_request", "mode must be agent|human|paused")
		return
	}
	s.SetMode(body.Mode)
	writeJSON(w, 200, s.Info())
}

func (a *API) trust(w http.ResponseWriter, r *http.Request) {
	s := a.session(w, r)
	if s == nil {
		return
	}
	rc := s.Resolved()
	if err := a.Mgr.KnownHosts().Delete(rc.Host + ":" + strconv.Itoa(rc.Port)); err != nil && !errors.Is(err, os.ErrNotExist) {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "the next connection will trust the presented certificate"})
}

func (a *API) emergencyStop(w http.ResponseWriter, r *http.Request) {
	a.Mgr.EmergencyStop()
	writeJSON(w, 200, map[string]string{"status": "all machines paused"})
}

func (a *API) screenshot(w http.ResponseWriter, r *http.Request) {
	s := a.session(w, r)
	if s == nil {
		return
	}
	maxW, _ := strconv.Atoi(r.URL.Query().Get("max_width"))
	format := r.URL.Query().Get("format")
	var shot *session.Shot
	err := s.Do(r.Context(), session.SourceAdmin, func(o *session.Ops) error {
		var err error
		shot, err = o.Screenshot(session.ShotOptions{MaxWidth: maxW, Format: format, KeepScale: true})
		return err
	})
	if err != nil {
		writeErr(w, 502, "capture_failed", err.Error())
		return
	}
	w.Header().Set("Content-Type", shot.MIME)
	w.Header().Set("X-Screen-Size", fmt.Sprintf("%dx%d", shot.ScreenWidth, shot.ScreenHeight))
	_, _ = w.Write(shot.Data)
}

func (a *API) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "internal", "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ch, cancel := a.Bus.Subscribe(256)
	defer cancel()

	snap := []session.Info{}
	for _, s := range a.Mgr.List() {
		snap = append(snap, s.Info())
	}
	writeSSE(w, "snapshot", map[string]any{"machines": snap})
	fl.Flush()

	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, ev.Data)
			fl.Flush()
		}
	}
}

func writeSSE(w http.ResponseWriter, typ string, v any) {
	b, _ := json.Marshal(v)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", typ, b)
}

func (a *API) audit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	recs, err := a.Audit.Query(q.Get("machine"), q.Get("tool"), limit)
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	if recs == nil {
		recs = []audit.Record{}
	}
	writeJSON(w, 200, recs)
}
