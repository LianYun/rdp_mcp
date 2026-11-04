package adminapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LianYun/rdp_mcp/internal/audit"
	"github.com/LianYun/rdp_mcp/internal/config"
	"github.com/LianYun/rdp_mcp/internal/events"
	"github.com/LianYun/rdp_mcp/internal/session"
)

type fakeKeychain map[string]string

func (f fakeKeychain) Get(k string) (string, error) { return f[k], nil }
func (f fakeKeychain) Set(k, v string) error        { f[k] = v; return nil }
func (f fakeKeychain) Delete(k string) error        { delete(f, k); return nil }

const baseYAML = `# top comment
server:
  listen: 127.0.0.1:0
defaults:
  width: 1280 # default width
machines:
  # first machine
  - name: a
    host: 10.0.0.1
    username: u
    password: ${PW}
`

func newTestAPI(t *testing.T) (*API, http.Handler, string, fakeKeychain) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "configs.yaml")
	if err := os.WriteFile(path, []byte(baseYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	mgr := session.NewManager(cfg, session.Options{DataDir: dir, Bus: bus, WorkerPath: "/nonexistent"})
	t.Cleanup(mgr.Shutdown)
	kc := fakeKeychain{}
	a := &API{Mgr: mgr, Bus: bus, Audit: audit.New(filepath.Join(dir, "audit"), true, false, 7, bus),
		AdminToken: "tok", MCPToken: func() string { return "" }, Shutdown: func() {}, Keychain: kc,
		LogFile: filepath.Join(dir, "missing.log")}
	return a, a.Handler(), path, kc
}

func do(t *testing.T, h http.Handler, method, url, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestAuthRequired(t *testing.T) {
	_, h, _, _ := newTestAPI(t)
	req := httptest.NewRequest("GET", "/api/v1/machines", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatal(rec.Code)
	}
	req = httptest.NewRequest("GET", "/api/v1/health", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatal("health should not need a token", rec.Code)
	}
}

func TestMachineCRUDPreservesComments(t *testing.T) {
	a, h, path, kc := newTestAPI(t)

	code, out := do(t, h, "POST", "/api/v1/machines",
		`{"name":"b","host":"10.0.0.2","username":"v","password":"s3cret","tags":["x"],"idle_disconnect":"5m"}`)
	if code != 201 || out["name"] != "b" {
		t.Fatalf("create: %d %v", code, out)
	}
	if kc["b"] != "s3cret" {
		t.Fatal("literal password not stored in keychain")
	}
	code, _ = do(t, h, "POST", "/api/v1/machines", `{"name":"b","host":"h","username":"u"}`)
	if code != 409 {
		t.Fatalf("duplicate: %d", code)
	}

	code, out = do(t, h, "PUT", "/api/v1/machines/b", `{"new_name":"c","host":"10.0.0.3","username":"v"}`)
	if code != 200 || out["name"] != "c" {
		t.Fatalf("rename: %d %v", code, out)
	}
	if _, err := a.Mgr.Lookup("b"); err == nil {
		t.Fatal("old name still present")
	}
	if kc["c"] != "s3cret" || kc["b"] != "" {
		t.Fatalf("keychain not migrated on rename: %v", kc)
	}

	raw, _ := os.ReadFile(path)
	s := string(raw)
	for _, want := range []string{"# top comment", "# default width", "# first machine", "password: keychain:c", "host: 10.0.0.3"} {
		if !strings.Contains(s, want) {
			t.Errorf("config lost %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "s3cret") {
		t.Fatal("plaintext password written to config")
	}

	code, out = do(t, h, "GET", "/api/v1/config", "")
	if code != 200 {
		t.Fatal(code)
	}
	ms := out["machines"].([]any)
	if len(ms) != 2 || ms[1].(map[string]any)["password"] != "keychain:c" {
		t.Fatalf("config view: %v", ms)
	}

	code, _ = do(t, h, "DELETE", "/api/v1/machines/c?delete_secret=true", "")
	if code != 200 {
		t.Fatal(code)
	}
	if _, ok := kc["c"]; ok {
		t.Fatal("secret not deleted")
	}
	if code, _ := do(t, h, "GET", "/api/v1/machines/c", ""); code != 404 {
		t.Fatal("deleted machine still served")
	}
}

func TestSectionUpdate(t *testing.T) {
	_, h, path, _ := newTestAPI(t)
	code, out := do(t, h, "PUT", "/api/v1/config/server", `{"ocr.languages":"eng","audit.retention_days":3,"log_level":"debug"}`)
	if code != 200 || out["restart_required"] != false {
		t.Fatalf("%d %v", code, out)
	}
	code, out = do(t, h, "PUT", "/api/v1/config/server", `{"listen":"127.0.0.1:9999"}`)
	if code != 200 || out["restart_required"] != true {
		t.Fatalf("%d %v", code, out)
	}
	if code, _ := do(t, h, "PUT", "/api/v1/config/server", `{"admin_token":"x"}`); code != 400 {
		t.Fatal("admin_token must not be writable", code)
	}
	if code, _ := do(t, h, "PUT", "/api/v1/config/defaults", `{"width":1600,"idle_disconnect":"0"}`); code != 200 {
		t.Fatal(code)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.OCR.Languages != "eng" || cfg.Server.Audit.RetentionDays != 3 || cfg.Defaults.Width != 1600 {
		t.Fatalf("not applied: %+v %+v", cfg.Server, cfg.Defaults)
	}
}

func TestRawConfigValidation(t *testing.T) {
	_, h, path, _ := newTestAPI(t)
	if code, _ := do(t, h, "PUT", "/api/v1/config/raw", "machines:\n  - name: x\n    hots: y\n"); code != 400 {
		t.Fatal("invalid YAML accepted", code)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "# top comment") {
		t.Fatal("file changed after rejected save")
	}
	if code, _ := do(t, h, "PUT", "/api/v1/config/raw", baseYAML+"  - name: z\n    host: h\n    username: u\n"); code != 200 {
		t.Fatal(code)
	}
}

func TestSecretAndCert(t *testing.T) {
	_, h, path, kc := newTestAPI(t)
	if code, _ := do(t, h, "PUT", "/api/v1/machines/a/secret", `{"password":"pw"}`); code != 200 {
		t.Fatal(code)
	}
	raw, _ := os.ReadFile(path)
	if kc["a"] != "pw" || !strings.Contains(string(raw), "password: keychain:a") {
		t.Fatal("secret not stored")
	}
	if code, _ := do(t, h, "POST", "/api/v1/machines/a/cert", `{"accept":"once"}`); code != 409 {
		t.Fatal("expected 409 without pending cert", code)
	}
}

func TestMCPTokenRotate(t *testing.T) {
	a, h, _, _ := newTestAPI(t)
	code, out := do(t, h, "POST", "/api/v1/service/mcp-token/rotate", "")
	if code != 200 || len(out["mcp_token"].(string)) < 20 {
		t.Fatal(code, out)
	}
	if a.Mgr.Config().Server.MCPToken != out["mcp_token"] {
		t.Fatal("token not applied")
	}
}

func TestDisabledMachine(t *testing.T) {
	_, h, _, _ := newTestAPI(t)
	code, out := do(t, h, "PUT", "/api/v1/machines/a", `{"host":"10.0.0.1","username":"u","enabled":false}`)
	if code != 200 || out["enabled"] != false {
		t.Fatalf("disable: %d %v", code, out)
	}
	if code, _ := do(t, h, "GET", "/api/v1/machines/a", ""); code != 200 {
		t.Fatalf("disabled machine not readable: %d", code)
	}
	code, out = do(t, h, "POST", "/api/v1/machines", `{"name":"d","host":"h","username":"u","enabled":false}`)
	if code != 201 || out["enabled"] != false {
		t.Fatalf("create disabled: %d %v", code, out)
	}
}
