package adminapi

import (
	"fmt"
	"os"
	"testing"

	"github.com/LianYun/rdp_mcp/internal/testutil/vncworker"
	"github.com/LianYun/rdp_mcp/internal/vnc/vnctest"
)

func TestMain(m *testing.M) {
	vncworker.MaybeRun()
	os.Exit(m.Run())
}

// POST /machines/test tries a definition from the form without saving it.
func TestTestUnsavedDefinition(t *testing.T) {
	srv := vnctest.Start(t, vnctest.Options{User: "alice", Pass: "pw"})
	t.Setenv(vncworker.Env, "1")
	t.Setenv("MACPW", "pw")
	a, h, path, _ := newTestAPI(t)
	before, _ := os.ReadFile(path)
	def := func(name, password string) string {
		pw := ""
		if password != "" {
			pw = fmt.Sprintf(`, "password": %q`, password)
		}
		return fmt.Sprintf(`{"name": %q, "protocol": "vnc", "host": "127.0.0.1", "port": %d, "username": "alice", "enable_ssh": false%s}`, name, srv.Port(), pw)
	}

	code, res := do(t, h, "POST", "/api/v1/machines/test", def("newmac", "pw"))
	if code != 200 || res["ok"] != true || res["screenshot"] == "" {
		t.Fatalf("unsaved test: %d %v", code, res["error"])
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("testing a definition changed the config file")
	}
	if len(a.Mgr.List()) != 1 {
		t.Fatal("testing a definition created a session")
	}

	// Editing a saved machine: an empty password means "use the saved one".
	code, _ = do(t, h, "POST", "/api/v1/machines", `{"name":"mac","protocol":"vnc","host":"127.0.0.1","username":"alice","password":"${MACPW}"}`)
	if code != 201 {
		t.Fatalf("create: %d", code)
	}
	if _, res = do(t, h, "POST", "/api/v1/machines/test", def("mac", "")); res["ok"] != true {
		t.Fatalf("saved password not used: %v", res["error"])
	}
	if _, res = do(t, h, "POST", "/api/v1/machines/test", def("mac", "wrong")); res["ok"] != false {
		t.Fatal("wrong password accepted")
	}
	if code, _ = do(t, h, "POST", "/api/v1/machines/test", `{"name":"x","host":""}`); code != 400 {
		t.Fatalf("missing host: %d", code)
	}
}
