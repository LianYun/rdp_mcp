package adminapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/LianYun/rdp_mcp/internal/config"
)

// configView is GET /api/v1/config: the effective config with secrets masked.
// References (keychain:..., ${VAR}) are shown as-is; literals become "***".
type configView struct {
	Path     string          `json:"path"`
	Server   serverView      `json:"server"`
	Defaults machineOptsView `json:"defaults"`
	Machines []machineView   `json:"machines"`
}

type serverView struct {
	Listen             string     `json:"listen"`
	MCPTokenSet        bool       `json:"mcp_token_set"`
	AdminTokenSet      bool       `json:"admin_token_set"`
	CORSOrigins        []string   `json:"cors_origins"`
	ScreenshotMaxWidth int        `json:"screenshot_max_width"`
	DrawCursor         *bool      `json:"draw_cursor,omitempty"`
	DataDir            string     `json:"data_dir"`
	WorkerPath         string     `json:"worker_path"`
	LogLevel           string     `json:"log_level"`
	Audit              auditView  `json:"audit"`
	OCR                config.OCR `json:"ocr"`
}

type auditView struct {
	Enabled         *bool `json:"enabled,omitempty"`
	Screenshots     *bool `json:"screenshots,omitempty"`
	RetentionDays   int   `json:"retention_days,omitempty"`
	RedactTypedText bool  `json:"redact_typed_text,omitempty"`
}

type machineOptsView struct {
	Port              int    `json:"port,omitempty"`
	Width             int    `json:"width,omitempty"`
	Height            int    `json:"height,omitempty"`
	Security          string `json:"security,omitempty"`
	Cert              string `json:"cert,omitempty"`
	AutoConnect       *bool  `json:"auto_connect,omitempty"`
	IdleDisconnect    string `json:"idle_disconnect,omitempty"`
	ReconnectAttempts *int   `json:"reconnect_attempts,omitempty"`
	EnableDrive       *bool  `json:"enable_drive,omitempty"`
	EnableClipboard   *bool  `json:"enable_clipboard,omitempty"`
	DisableWallpaper  *bool  `json:"disable_wallpaper,omitempty"`
}

type machineView struct {
	Name            string   `json:"name"`
	Description     string   `json:"description,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	Enabled         *bool    `json:"enabled,omitempty"`
	Host            string   `json:"host"`
	Username        string   `json:"username,omitempty"`
	Domain          string   `json:"domain,omitempty"`
	Password        string   `json:"password,omitempty"`
	PasswordCmd     string   `json:"password_cmd,omitempty"`
	CertFingerprint string   `json:"cert_fingerprint,omitempty"`
	machineOptsView
}

func maskPassword(pw string) string {
	if pw == "" || config.IsPasswordRef(pw) {
		return pw
	}
	return "***"
}

func optsView(o config.MachineOptions) machineOptsView {
	v := machineOptsView{
		Port: o.Port, Width: o.Width, Height: o.Height,
		Security: o.Security, Cert: o.Cert,
		AutoConnect: o.AutoConnect, EnableDrive: o.EnableDrive,
		EnableClipboard: o.EnableClipboard, DisableWallpaper: o.DisableWallpaper,
		ReconnectAttempts: o.ReconnectAttempts,
	}
	if o.IdleDisconnect != nil {
		v.IdleDisconnect = o.IdleDisconnect.String()
	}
	return v
}

func (a *API) configView() configView {
	cfg := a.Mgr.Config()
	out := configView{Path: cfg.Path}
	s := cfg.Server
	out.Server = serverView{
		Listen: s.Listen, MCPTokenSet: s.MCPToken != "", AdminTokenSet: s.AdminToken != "",
		CORSOrigins: s.CORSOrigins, ScreenshotMaxWidth: s.ScreenshotMaxWidth,
		DrawCursor: s.DrawCursor, DataDir: s.DataDir, WorkerPath: s.WorkerPath,
		LogLevel: s.LogLevel, OCR: s.OCR, Audit: auditView{
			Enabled: s.Audit.Enabled, Screenshots: s.Audit.Screenshots,
			RetentionDays: s.Audit.RetentionDays, RedactTypedText: s.Audit.RedactTypedText,
		},
	}
	if out.Server.Listen == "" {
		out.Server.Listen = config.DefaultListen
	}
	out.Defaults = optsView(cfg.Defaults)
	for _, m := range cfg.Machines {
		out.Machines = append(out.Machines, machineView{
			Name: m.Name, Description: m.Description, Tags: m.Tags, Enabled: m.Enabled,
			Host: m.Host, Username: m.Username, Domain: m.Domain,
			Password: maskPassword(m.Password), PasswordCmd: maskPassword(m.PasswordCmd),
			CertFingerprint: m.CertFingerprint, machineOptsView: optsView(m.MachineOptions),
		})
	}
	return out
}

func (a *API) getConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, a.configView())
}

func (a *API) getConfigRaw(w http.ResponseWriter, r *http.Request) {
	b, err := os.ReadFile(a.Mgr.Config().Path)
	if err != nil && !os.IsNotExist(err) {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.WriteHeader(200)
	_, _ = w.Write(b)
}

func (a *API) putConfigRaw(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, 400, "bad_request", err.Error())
		return
	}
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	cfg, err := config.SaveRaw(a.Mgr.Config().Path, raw)
	if err != nil {
		writeConfigErr(w, err)
		return
	}
	a.apply(cfg)
	writeJSON(w, 200, map[string]any{"status": "saved", "machines": len(cfg.Machines)})
}

// sectionKeys lists the keys PUT /config/server|defaults may change. Values are
// scalars or flat string lists; null removes the key (falls back to the default).
var sectionKeys = map[string]map[string]bool{
	"server": {
		"listen": true, "cors_origins": true, "screenshot_max_width": true, "draw_cursor": true,
		"data_dir": true, "worker_path": true, "log_level": true,
		"audit.enabled": true, "audit.screenshots": true, "audit.retention_days": true, "audit.redact_typed_text": true,
		"ocr.enabled": true, "ocr.tesseract": true, "ocr.languages": true,
	},
	"defaults": {
		"port": true, "width": true, "height": true, "security": true, "cert": true,
		"auto_connect": true, "idle_disconnect": true, "reconnect_attempts": true,
		"enable_drive": true, "enable_clipboard": true, "disable_wallpaper": true,
	},
}

// restartKeys need a service restart to take effect.
var restartKeys = map[string]bool{"listen": true, "data_dir": true, "worker_path": true, "cors_origins": true}

func (a *API) putSection(section string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, 400, "bad_request", err.Error())
			return
		}
		restart := false
		for k, v := range body {
			if !sectionKeys[section][k] {
				writeErr(w, 400, "bad_request", fmt.Sprintf("%s.%s cannot be changed here (use /config/raw)", section, k))
				return
			}
			if f, ok := v.(float64); ok && f == float64(int64(f)) {
				body[k] = int64(f) // JSON numbers -> YAML integers
			}
			if list, ok := v.([]any); ok {
				strs := make([]string, 0, len(list))
				for _, it := range list {
					str, ok := it.(string)
					if !ok {
						writeErr(w, 400, "bad_request", fmt.Sprintf("%s.%s must be a list of strings", section, k))
						return
					}
					strs = append(strs, str)
				}
				body[k] = strs
			}
			if section == "server" && restartKeys[k] {
				restart = true
			}
		}
		a.cfgMu.Lock()
		defer a.cfgMu.Unlock()
		cfg, err := config.UpdateSection(a.Mgr.Config().Path, section, body)
		if err != nil {
			writeConfigErr(w, err)
			return
		}
		a.apply(cfg)
		writeJSON(w, 200, map[string]any{"status": "saved", "restart_required": restart})
	}
}

// logs serves GET /logs?level=&limit=&since=: the tail of the service log.
func (a *API) logs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	minLevel := levelRank(q.Get("level"))
	var since time.Time
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeErr(w, 400, "bad_request", "since must be RFC3339")
			return
		}
		since = t
	}
	lines := []string{}
	if a.LogFile != "" {
		lines = tailLines(a.LogFile, 4<<20)
	}
	out := make([]string, 0, limit)
	for i := len(lines) - 1; i >= 0 && len(out) < limit; i-- {
		ln := lines[i]
		if minLevel > 0 && levelRank(fieldValue(ln, "level")) < minLevel {
			continue
		}
		if !since.IsZero() {
			if t, err := time.Parse(time.RFC3339Nano, fieldValue(ln, "time")); err == nil && t.Before(since) {
				continue
			}
		}
		out = append(out, ln)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	writeJSON(w, 200, map[string]any{"file": a.LogFile, "lines": out})
}

func levelRank(l string) int {
	switch strings.ToUpper(l) {
	case "DEBUG":
		return 1
	case "INFO":
		return 2
	case "WARN", "WARNING":
		return 3
	case "ERROR":
		return 4
	}
	return 0
}

// fieldValue extracts key=value from a slog text line.
func fieldValue(line, key string) string {
	i := strings.Index(line, key+"=")
	if i < 0 || (i > 0 && line[i-1] != ' ') {
		return ""
	}
	v := line[i+len(key)+1:]
	if j := strings.IndexByte(v, ' '); j >= 0 {
		v = v[:j]
	}
	return v
}

// tailLines returns the lines of the last maxBytes of a file.
func tailLines(path string, maxBytes int64) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil
	}
	off := st.Size() - maxBytes
	if off < 0 {
		off = 0
	}
	b := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(b, off); err != nil && err != io.EOF {
		return nil
	}
	s := strings.TrimRight(string(b), "\n")
	if off > 0 {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
	}
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// auditThumb serves GET /audit/thumbs/{file}.
func (a *API) auditThumb(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	if !thumbName.MatchString(name) {
		writeErr(w, 404, "not_found", "no such thumbnail")
		return
	}
	b, err := os.ReadFile(filepath.Join(a.Audit.ThumbDir(), name))
	if err != nil {
		writeErr(w, 404, "not_found", "no such thumbnail")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "max-age=86400")
	_, _ = w.Write(b)
}

var thumbName = regexp.MustCompile(`^tc_[0-9]+_[0-9]+\.jpg$`)
