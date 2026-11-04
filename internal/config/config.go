// Package config loads and validates .rdp_mcp/configs.yaml. See docs/configuration.md.
package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	DefaultListen   = "127.0.0.1:8765"
	FileName        = "configs.yaml"
	DirName         = ".rdp_mcp"
	KeychainService = "rdp_mcp"
)

// Duration accepts "30m", "1h", "0".
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	if s == "" || s == "0" {
		d.Duration = 0
		return nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q", s)
	}
	d.Duration = v
	return nil
}

func (d Duration) MarshalYAML() (any, error) {
	if d.Duration == 0 {
		return "0", nil
	}
	h := d.Duration / time.Hour
	m := (d.Duration % time.Hour) / time.Minute
	s := (d.Duration % time.Minute) / time.Second
	out := ""
	if h > 0 {
		out += fmt.Sprintf("%dh", h)
	}
	if m > 0 {
		out += fmt.Sprintf("%dm", m)
	}
	if s > 0 || out == "" {
		out += fmt.Sprintf("%ds", s)
	}
	return out, nil
}

type Audit struct {
	Enabled         *bool `yaml:"enabled,omitempty" json:"enabled"`
	Screenshots     *bool `yaml:"screenshots,omitempty" json:"screenshots"`
	RetentionDays   int   `yaml:"retention_days,omitempty" json:"retention_days"`
	RedactTypedText bool  `yaml:"redact_typed_text,omitempty" json:"redact_typed_text"`
}

type Server struct {
	Listen             string   `yaml:"listen,omitempty" json:"listen"`
	MCPToken           string   `yaml:"mcp_token,omitempty" json:"-"`
	AdminToken         string   `yaml:"admin_token,omitempty" json:"-"`
	CORSOrigins        []string `yaml:"cors_origins,omitempty" json:"cors_origins"`
	ScreenshotMaxWidth int      `yaml:"screenshot_max_width,omitempty" json:"screenshot_max_width"`
	DrawCursor         *bool    `yaml:"draw_cursor,omitempty" json:"draw_cursor"`
	DataDir            string   `yaml:"data_dir,omitempty" json:"data_dir"`
	WorkerPath         string   `yaml:"worker_path,omitempty" json:"worker_path"`
	LogLevel           string   `yaml:"log_level,omitempty" json:"log_level"`
	Audit              Audit    `yaml:"audit,omitempty" json:"audit"`
	OCR                OCR      `yaml:"ocr,omitempty" json:"ocr"`
}

// OCR configures server-side text recognition on screenshots (docs/design.md D16).
type OCR struct {
	Enabled   *bool  `yaml:"enabled,omitempty" json:"enabled"`
	Tesseract string `yaml:"tesseract,omitempty" json:"tesseract"` // path; default: found in PATH
	Languages string `yaml:"languages,omitempty" json:"languages"` // e.g. "chi_sim+eng"; default: auto
}

// MachineOptions are fields that may appear in defaults and per machine.
type MachineOptions struct {
	Port              int       `yaml:"port,omitempty" json:"port,omitempty"`
	Width             int       `yaml:"width,omitempty" json:"width,omitempty"`
	Height            int       `yaml:"height,omitempty" json:"height,omitempty"`
	Security          string    `yaml:"security,omitempty" json:"security,omitempty"`
	Cert              string    `yaml:"cert,omitempty" json:"cert,omitempty"`
	AutoConnect       *bool     `yaml:"auto_connect,omitempty" json:"auto_connect,omitempty"`
	IdleDisconnect    *Duration `yaml:"idle_disconnect,omitempty" json:"-"`
	ReconnectAttempts *int      `yaml:"reconnect_attempts,omitempty" json:"reconnect_attempts,omitempty"`
	EnableDrive       *bool     `yaml:"enable_drive,omitempty" json:"enable_drive,omitempty"`
	EnableClipboard   *bool     `yaml:"enable_clipboard,omitempty" json:"enable_clipboard,omitempty"`
	DisableWallpaper  *bool     `yaml:"disable_wallpaper,omitempty" json:"disable_wallpaper,omitempty"`
}

type Machine struct {
	Name            string   `yaml:"name" json:"name"`
	Description     string   `yaml:"description,omitempty" json:"description,omitempty"`
	Tags            []string `yaml:"tags,omitempty" json:"tags,omitempty"`
	Enabled         *bool    `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Host            string   `yaml:"host" json:"host"`
	Username        string   `yaml:"username" json:"username"`
	Password        string   `yaml:"password,omitempty" json:"-"`
	PasswordCmd     string   `yaml:"password_cmd,omitempty" json:"-"`
	Domain          string   `yaml:"domain,omitempty" json:"domain,omitempty"`
	CertFingerprint string   `yaml:"cert_fingerprint,omitempty" json:"cert_fingerprint,omitempty"`
	MachineOptions  `yaml:",inline"`
}

type Config struct {
	Server   Server         `yaml:"server" json:"server"`
	Defaults MachineOptions `yaml:"defaults" json:"defaults"`
	Machines []Machine      `yaml:"machines" json:"machines"`

	// Path of the file this config was loaded from.
	Path string `yaml:"-" json:"path"`
}

// Resolved holds effective values for a machine after applying defaults.
type Resolved struct {
	Machine
	Port              int
	Width             int
	Height            int
	Security          string
	Cert              string
	AutoConnect       bool
	IdleDisconnect    time.Duration
	ReconnectAttempts int
	EnableDrive       bool
	EnableClipboard   bool
	DisableWallpaper  bool
	IsEnabled         bool
}

func boolOr(vals ...*bool) bool {
	for _, v := range vals[:len(vals)-1] {
		if v != nil {
			return *v
		}
	}
	return *vals[len(vals)-1]
}

func ptr[T any](v T) *T { return &v }

func firstNonZero[T comparable](vals ...T) T {
	var zero T
	for _, v := range vals {
		if v != zero {
			return v
		}
	}
	return zero
}

// Resolve applies defaults to machine m.
func (c *Config) Resolve(m Machine) Resolved {
	d := c.Defaults
	r := Resolved{Machine: m}
	r.Port = firstNonZero(m.Port, d.Port, 3389)
	r.Width = firstNonZero(m.Width, d.Width, 1920)
	r.Height = firstNonZero(m.Height, d.Height, 1080)
	r.Security = firstNonZero(m.Security, d.Security, "auto")
	r.Cert = firstNonZero(m.Cert, d.Cert, "tofu")
	r.AutoConnect = boolOr(m.AutoConnect, d.AutoConnect, ptr(false))
	r.EnableDrive = boolOr(m.EnableDrive, d.EnableDrive, ptr(true))
	r.EnableClipboard = boolOr(m.EnableClipboard, d.EnableClipboard, ptr(true))
	r.DisableWallpaper = boolOr(m.DisableWallpaper, d.DisableWallpaper, ptr(true))
	r.IsEnabled = boolOr(m.Enabled, ptr(true))
	switch {
	case m.IdleDisconnect != nil:
		r.IdleDisconnect = m.IdleDisconnect.Duration
	case d.IdleDisconnect != nil:
		r.IdleDisconnect = d.IdleDisconnect.Duration
	default:
		r.IdleDisconnect = 30 * time.Minute
	}
	switch {
	case m.ReconnectAttempts != nil:
		r.ReconnectAttempts = *m.ReconnectAttempts
	case d.ReconnectAttempts != nil:
		r.ReconnectAttempts = *d.ReconnectAttempts
	default:
		r.ReconnectAttempts = 5
	}
	return r
}

func (c *Config) Machine(name string) (Machine, bool) {
	for _, m := range c.Machines {
		if m.Name == name {
			return m, true
		}
	}
	return Machine{}, false
}

// Effective server values.
func (s Server) EffectiveListen() string { return firstNonZero(s.Listen, DefaultListen) }
func (s Server) EffectiveMaxWidth() int  { return firstNonZero(s.ScreenshotMaxWidth, 1280) }
func (s Server) EffectiveDrawCursor() bool {
	return s.DrawCursor == nil || *s.DrawCursor
}
func (s Server) OCREnabled() bool       { return s.OCR.Enabled == nil || *s.OCR.Enabled }
func (s Server) AuditEnabled() bool     { return s.Audit.Enabled == nil || *s.Audit.Enabled }
func (s Server) AuditScreenshots() bool { return s.Audit.Screenshots == nil || *s.Audit.Screenshots }
func (s Server) AuditRetentionDays() int {
	return firstNonZero(s.Audit.RetentionDays, 7)
}

// EffectiveDataDir returns the absolute data directory.
func (s Server) EffectiveDataDir() string {
	dir := s.DataDir
	if dir == "" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, DirName)
	}
	return ExpandHome(dir)
}

func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return p
}

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

func expandEnv(s string) string {
	return envRef.ReplaceAllStringFunc(s, func(m string) string {
		return os.Getenv(envRef.FindStringSubmatch(m)[1])
	})
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Validate checks the config for errors; warnings are returned separately.
func (c *Config) Validate() (warnings []string, err error) {
	var errs []error
	seen := map[string]bool{}
	for i, m := range c.Machines {
		where := fmt.Sprintf("machines[%d]", i)
		if m.Name != "" {
			where = fmt.Sprintf("machine %q", m.Name)
		}
		if !nameRe.MatchString(m.Name) {
			errs = append(errs, fmt.Errorf("%s: name must match [A-Za-z0-9_-]{1,64}", where))
		}
		if seen[m.Name] {
			errs = append(errs, fmt.Errorf("%s: duplicate name", where))
		}
		seen[m.Name] = true
		if m.Host == "" {
			errs = append(errs, fmt.Errorf("%s: host is required", where))
		}
		if m.Username == "" {
			errs = append(errs, fmt.Errorf("%s: username is required", where))
		}
		if m.Password != "" && m.PasswordCmd != "" {
			errs = append(errs, fmt.Errorf("%s: password and password_cmd are mutually exclusive", where))
		}
		if m.Password == "" && m.PasswordCmd == "" {
			warnings = append(warnings, fmt.Sprintf("%s: no password configured", where))
		}
		if m.Password != "" && !strings.HasPrefix(m.Password, "keychain:") && !envRef.MatchString(m.Password) {
			warnings = append(warnings, fmt.Sprintf("%s: plaintext password in config; prefer keychain:, ${ENV} or password_cmd", where))
		}
		r := c.Resolve(m)
		switch r.Security {
		case "auto", "nla", "tls", "rdp":
		default:
			errs = append(errs, fmt.Errorf("%s: security must be auto|nla|tls|rdp", where))
		}
		switch r.Cert {
		case "tofu", "ignore":
		case "fingerprint":
			if m.CertFingerprint == "" {
				errs = append(errs, fmt.Errorf("%s: cert: fingerprint requires cert_fingerprint", where))
			}
		default:
			errs = append(errs, fmt.Errorf("%s: cert must be tofu|ignore|fingerprint", where))
		}
		if r.Width < 200 || r.Height < 200 || r.Width > 8192 || r.Height > 8192 {
			errs = append(errs, fmt.Errorf("%s: width/height out of range 200..8192", where))
		}
	}
	if !isLoopback(c.Server.EffectiveListen()) {
		if c.Server.MCPToken == "" {
			errs = append(errs, errors.New("server.mcp_token is required when listening on a non-loopback address"))
		}
		if c.Server.AdminToken == "" {
			errs = append(errs, errors.New("server.admin_token is required when listening on a non-loopback address"))
		}
	}
	return warnings, errors.Join(errs...)
}

func isLoopback(addr string) bool {
	host := addr
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		host = addr[:i]
	}
	host = strings.Trim(host, "[]")
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

// Find returns the config path to use: explicit, ./.rdp_mcp/configs.yaml, ~/.rdp_mcp/configs.yaml.
func Find(explicit string) string {
	if explicit != "" {
		return ExpandHome(explicit)
	}
	if wd, err := os.Getwd(); err == nil {
		p := filepath.Join(wd, DirName, FileName)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, DirName, FileName)
}

// Load reads and parses the config at path. A missing file yields an empty config.
func Load(path string) (*Config, error) {
	c := &Config{Path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	return Parse(data, path)
}

func Parse(data []byte, path string) (*Config, error) {
	c := &Config{Path: path}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(c); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	c.Server.MCPToken = expandEnv(c.Server.MCPToken)
	c.Server.AdminToken = expandEnv(c.Server.AdminToken)
	return c, nil
}

// ResolvePassword returns the plaintext password for m.
func ResolvePassword(ctx context.Context, m Machine, keychainGet func(key string) (string, error)) (string, error) {
	switch {
	case m.PasswordCmd != "":
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", m.PasswordCmd)
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("password_cmd failed: %w", err)
		}
		return strings.TrimRight(string(out), "\r\n"), nil
	case strings.HasPrefix(m.Password, "keychain:"):
		key := strings.TrimPrefix(m.Password, "keychain:")
		if key == "" {
			key = m.Name
		}
		pw, err := keychainGet(key)
		if err != nil {
			return "", fmt.Errorf("read keychain entry %q: %w", key, err)
		}
		return pw, nil
	default:
		return expandEnv(m.Password), nil
	}
}
