package adminapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/LianYun/rdp_mcp/internal/config"
	"github.com/LianYun/rdp_mcp/internal/runtime"
)

// SecretStore is the keychain used for machine passwords (internal/secrets in production).
type SecretStore interface {
	Set(key, password string) error
	Get(key string) (string, error)
	Delete(key string) error
}

// machineRequest is the body of POST /machines and PUT /machines/{name} (docs/admin-api.md).
// Password semantics: password omitted = keep the previous setting (PUT only);
// password "" = remove; a keychain:/${ENV} reference is stored as-is; any other
// non-empty value is stored in the keychain and referenced as keychain:<name>.
type machineRequest struct {
	Name              string   `json:"name"`
	NewName           string   `json:"new_name"`
	Description       string   `json:"description"`
	Tags              []string `json:"tags"`
	Enabled           *bool    `json:"enabled"`
	Host              string   `json:"host"`
	Port              int      `json:"port"`
	Username          string   `json:"username"`
	Domain            string   `json:"domain"`
	Password          *string  `json:"password"`
	PasswordCmd       *string  `json:"password_cmd"`
	Width             int      `json:"width"`
	Height            int      `json:"height"`
	Security          string   `json:"security"`
	Cert              string   `json:"cert"`
	CertFingerprint   string   `json:"cert_fingerprint"`
	AutoConnect       *bool    `json:"auto_connect"`
	IdleDisconnect    string   `json:"idle_disconnect"`
	ReconnectAttempts *int     `json:"reconnect_attempts"`
	EnableDrive       *bool    `json:"enable_drive"`
	EnableClipboard   *bool    `json:"enable_clipboard"`
	DisableWallpaper  *bool    `json:"disable_wallpaper"`
}

func (rq *machineRequest) toMachine() (config.Machine, error) {
	m := config.Machine{
		Name: rq.Name, Description: rq.Description, Tags: rq.Tags, Enabled: rq.Enabled,
		Host: rq.Host, Username: rq.Username, Domain: rq.Domain,
		CertFingerprint: rq.CertFingerprint,
		MachineOptions: config.MachineOptions{
			Port: rq.Port, Width: rq.Width, Height: rq.Height,
			Security: rq.Security, Cert: rq.Cert,
			AutoConnect:       rq.AutoConnect,
			ReconnectAttempts: rq.ReconnectAttempts,
			EnableDrive:       rq.EnableDrive,
			EnableClipboard:   rq.EnableClipboard,
			DisableWallpaper:  rq.DisableWallpaper,
		},
	}
	if rq.IdleDisconnect != "" {
		d, err := time.ParseDuration(rq.IdleDisconnect)
		if err != nil {
			return config.Machine{}, fmt.Errorf("idle_disconnect: %w", err)
		}
		m.IdleDisconnect = &config.Duration{Duration: d}
	}
	return m, nil
}

// applyPassword resolves the password fields of the request against the previous
// definition, storing literal passwords in the keychain.
func (a *API) applyPassword(m *config.Machine, old config.Machine, rq *machineRequest) error {
	switch {
	case rq.Password != nil && *rq.Password != "":
		pw := *rq.Password
		if config.IsPasswordRef(pw) {
			m.Password = pw
			break
		}
		if err := a.Keychain.Set(m.Name, pw); err != nil {
			return fmt.Errorf("store password in keychain: %w", err)
		}
		m.Password = "keychain:" + m.Name
	case rq.PasswordCmd != nil && *rq.PasswordCmd != "":
		m.PasswordCmd = *rq.PasswordCmd
	case rq.Password != nil: // explicit "" clears the password
	case rq.PasswordCmd != nil: // explicit "" clears password_cmd
	default: // both omitted: keep the previous references
		m.Password, m.PasswordCmd = old.Password, old.PasswordCmd
	}
	return nil
}

// syncPasswordKeys writes m's password fields to the machine entry when they differ
// (UpdateMachine itself never touches password keys).
func (a *API) syncPasswordKeys(path, name string, cfg *config.Config, m config.Machine) (*config.Config, error) {
	cur, ok := cfg.Machine(name)
	if !ok {
		return cfg, nil
	}
	set := map[string]any{}
	if cur.Password != m.Password {
		set["password"] = nil
		if m.Password != "" {
			set["password"] = m.Password
		}
	}
	if cur.PasswordCmd != m.PasswordCmd {
		set["password_cmd"] = nil
		if m.PasswordCmd != "" {
			set["password_cmd"] = m.PasswordCmd
		}
	}
	if len(set) == 0 {
		return cfg, nil
	}
	return config.UpdateMachineFields(path, name, set)
}

func writeConfigErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, os.ErrNotExist):
		writeErr(w, 404, "not_found", err.Error())
	case strings.Contains(err.Error(), "already exists"):
		writeErr(w, 409, "conflict", err.Error())
	default:
		writeErr(w, 400, "invalid_config", err.Error())
	}
}

func (a *API) createMachine(w http.ResponseWriter, r *http.Request) {
	var rq machineRequest
	if err := json.NewDecoder(r.Body).Decode(&rq); err != nil {
		writeErr(w, 400, "bad_request", err.Error())
		return
	}
	if rq.Name == "" {
		writeErr(w, 400, "bad_request", "name is required")
		return
	}
	m, err := rq.toMachine()
	if err != nil {
		writeErr(w, 400, "bad_request", err.Error())
		return
	}
	if err := a.applyPassword(&m, config.Machine{}, &rq); err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	cfg, err := config.AddMachine(a.Mgr.Config().Path, m)
	if err != nil {
		writeConfigErr(w, err)
		return
	}
	a.apply(cfg)
	s, err := a.Mgr.Get(m.Name)
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	writeJSON(w, 201, s.Info())
}

func (a *API) updateMachine(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var rq machineRequest
	if err := json.NewDecoder(r.Body).Decode(&rq); err != nil {
		writeErr(w, 400, "bad_request", err.Error())
		return
	}
	cur, ok := a.Mgr.Config().Machine(name)
	if !ok {
		writeErr(w, 404, "not_found", fmt.Sprintf("unknown machine %q", name))
		return
	}
	newName := name
	if rq.NewName != "" {
		newName = rq.NewName
	}
	m, err := rq.toMachine()
	if err != nil {
		writeErr(w, 400, "bad_request", err.Error())
		return
	}
	m.Name = newName
	if err := a.applyPassword(&m, cur, &rq); err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}

	// Renaming a machine whose password lives under its own keychain key moves the
	// secret to the new key so the reference keeps pointing at the machine.
	migrateSecret := newName != name && m.Password == "keychain:"+name
	if migrateSecret {
		m.Password = "keychain:" + newName
	}

	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	cfg, err := config.UpdateMachine(a.Mgr.Config().Path, name, m)
	if err != nil {
		writeConfigErr(w, err)
		return
	}
	if migrateSecret {
		if pw, err := a.Keychain.Get(name); err == nil && pw != "" {
			if err := a.Keychain.Set(newName, pw); err == nil {
				_ = a.Keychain.Delete(name)
			}
		}
	}
	cfg, err = a.syncPasswordKeys(a.Mgr.Config().Path, newName, cfg, m)
	if err != nil {
		writeConfigErr(w, err)
		return
	}
	a.apply(cfg)
	s, err := a.Mgr.Get(newName)
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	writeJSON(w, 200, s.Info())
}

func (a *API) deleteMachine(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, ok := a.Mgr.Config().Machine(name); !ok {
		writeErr(w, 404, "not_found", fmt.Sprintf("unknown machine %q", name))
		return
	}
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	cfg, err := config.DeleteMachine(a.Mgr.Config().Path, name)
	if err != nil {
		writeConfigErr(w, err)
		return
	}
	if r.URL.Query().Get("delete_secret") == "true" {
		if err := a.Keychain.Delete(name); err != nil {
			a.Mgr.Bus().Publish("log", map[string]string{"level": "warn", "msg": "keychain delete: " + err.Error()})
		}
	}
	a.apply(cfg)
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

func (a *API) putSecret(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, ok := a.Mgr.Config().Machine(name); !ok {
		writeErr(w, 404, "not_found", fmt.Sprintf("unknown machine %q", name))
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad_request", err.Error())
		return
	}
	if body.Password == "" {
		writeErr(w, 400, "bad_request", "password is required")
		return
	}
	if err := a.Keychain.Set(name, body.Password); err != nil {
		writeErr(w, 500, "internal", "keychain: "+err.Error())
		return
	}
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	cfg, err := config.UpdateMachineFields(a.Mgr.Config().Path, name,
		map[string]any{"password": "keychain:" + name, "password_cmd": nil})
	if err != nil {
		writeConfigErr(w, err)
		return
	}
	a.apply(cfg)
	writeJSON(w, 200, map[string]string{"status": "stored", "password": "keychain:" + name})
}

func (a *API) testMachine(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	cur, ok := a.Mgr.Config().Machine(name)
	if !ok {
		writeErr(w, 404, "not_found", fmt.Sprintf("unknown machine %q", name))
		return
	}
	var body struct {
		Host     *string `json:"host"`
		Port     *int    `json:"port"`
		Username *string `json:"username"`
		Password *string `json:"password"`
		Security *string `json:"security"`
		Width    *int    `json:"width"`
		Height   *int    `json:"height"`
	}
	if raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20)); len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			writeErr(w, 400, "bad_request", err.Error())
			return
		}
	}
	mc := cur
	if body.Host != nil {
		mc.Host = *body.Host
	}
	if body.Port != nil {
		mc.Port = *body.Port
	}
	if body.Username != nil {
		mc.Username = *body.Username
	}
	if body.Password != nil && *body.Password != "" {
		mc.Password = *body.Password // literal, not saved anywhere
		mc.PasswordCmd = ""
	}
	if body.Security != nil {
		mc.Security = *body.Security
	}
	if body.Width != nil {
		mc.Width = *body.Width
	}
	if body.Height != nil {
		mc.Height = *body.Height
	}
	res := a.Mgr.TestConnect(r.Context(), mc)
	writeJSON(w, 200, res)
}

func (a *API) answerCert(w http.ResponseWriter, r *http.Request) {
	s := a.session(w, r)
	if s == nil {
		return
	}
	var body struct {
		Accept string `json:"accept"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad_request", err.Error())
		return
	}
	if err := s.AnswerCert(body.Accept); err != nil {
		writeErr(w, 409, "no_pending_cert", err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": body.Accept})
}

func (a *API) rotateMCPToken(w http.ResponseWriter, r *http.Request) {
	token := runtime.NewToken()
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	cfg, err := config.SetMCPToken(a.Mgr.Config().Path, token)
	if err != nil {
		writeConfigErr(w, err)
		return
	}
	a.apply(cfg)
	writeJSON(w, 200, map[string]string{"mcp_token": token})
}

// apply activates a freshly written config. Nil-safe for tests.
func (a *API) apply(cfg *config.Config) {
	if a.Apply != nil {
		a.Apply(cfg)
	} else {
		a.Mgr.UpdateConfig(cfg)
	}
	if a.Bus != nil {
		a.Bus.Publish("config_changed", map[string]any{"machines": len(cfg.Machines), "source": "api"})
	}
}
