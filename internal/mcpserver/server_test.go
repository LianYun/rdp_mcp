package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LianYun/rdp_mcp/internal/audit"
	"github.com/LianYun/rdp_mcp/internal/config"
	"github.com/LianYun/rdp_mcp/internal/events"
	"github.com/LianYun/rdp_mcp/internal/session"
	"github.com/LianYun/rdp_mcp/internal/testutil/fakeworker"
)

func TestMain(m *testing.M) {
	fakeworker.MaybeRun()
	os.Exit(m.Run())
}

const testYAML = `
defaults:
  enable_drive: false
machines:
  - name: m1
    description: test machine
    host: 127.0.0.1
    username: u
    password: p
  - name: hidden
    host: 127.0.0.1
    username: u
    enabled: false
`

func setup(t *testing.T) (*mcp.ClientSession, *session.Manager, *audit.Logger) {
	t.Helper()
	dir := t.TempDir()
	for _, kv := range fakeworker.Env(filepath.Join(dir, "input.jsonl")) {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	cfg, err := config.Parse([]byte(testYAML), filepath.Join(dir, "configs.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	bus := events.NewBus()
	mgr := session.NewManager(cfg, session.Options{WorkerPath: exe, DataDir: dir, Bus: bus})
	t.Cleanup(mgr.Shutdown)
	al := audit.New(filepath.Join(dir, "audit"), true, false, 7, bus)

	srv := New(mgr, al, "test").MCP()
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, mgr, al
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var text strings.Builder
	hasImage := false
	for _, c := range res.Content {
		switch v := c.(type) {
		case *mcp.TextContent:
			text.WriteString(v.Text)
		case *mcp.ImageContent:
			hasImage = len(v.Data) > 0
		}
	}
	return res, text.String(), hasImage
}

func TestToolList(t *testing.T) {
	cs, _, _ := setup(t)
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range tools.Tools {
		names = append(names, tl.Name)
	}
	got := strings.Join(names, ",")
	for _, want := range []string{"list_machines", "screenshot", "mouse", "keyboard", "clipboard", "run_command", "job", "file", "session", "wait"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing tool %s in %s", want, got)
		}
	}
}

func TestDesktopTools(t *testing.T) {
	cs, _, al := setup(t)

	_, text, _ := call(t, cs, "list_machines", nil)
	if !strings.Contains(text, "m1") || strings.Contains(text, "hidden") {
		t.Fatalf("list_machines: %s", text)
	}

	res, text, img := call(t, cs, "screenshot", map[string]any{"machine": "m1"})
	if res.IsError || !img || !strings.Contains(text, "screen 1920x1080") {
		t.Fatalf("screenshot: err=%v img=%v %s", res.IsError, img, text)
	}

	res, text, img = call(t, cs, "mouse", map[string]any{"machine": "m1", "action": "double_click", "x": 100, "y": 100})
	if res.IsError || !img || !strings.HasPrefix(text, "ok: double_click") {
		t.Fatalf("mouse: %v %s", res.IsError, text)
	}

	res, text, img = call(t, cs, "keyboard", map[string]any{"machine": "m1", "action": "type", "text": "héllo\n", "screenshot_after": false})
	if res.IsError || img || !strings.Contains(text, "typed 6 characters") {
		t.Fatalf("keyboard: %v %s", res.IsError, text)
	}

	res, text, _ = call(t, cs, "keyboard", map[string]any{"machine": "m1", "action": "hotkey", "keys": []string{"ctrl", "nosuchkey"}})
	if !res.IsError || !strings.Contains(text, "unknown key") {
		t.Fatalf("bad key not rejected: %s", text)
	}

	call(t, cs, "clipboard", map[string]any{"machine": "m1", "action": "set", "text": "abc"})
	if _, text, _ = call(t, cs, "clipboard", map[string]any{"machine": "m1", "action": "get"}); text != "abc" {
		t.Fatalf("clipboard get: %q", text)
	}

	if _, text, _ = call(t, cs, "session", map[string]any{"machine": "m1", "action": "set_resolution", "width": 1600, "height": 900}); !strings.Contains(text, "1600x900") {
		t.Fatalf("set_resolution: %s", text)
	}

	if res, text, img = call(t, cs, "wait", map[string]any{"machine": "m1", "until_stable": true, "seconds": 2}); res.IsError || !img || !strings.Contains(text, "stable") {
		t.Fatalf("wait: %s", text)
	}

	// OCR: the fake desktop has no text, so expect an empty line list (or an install hint).
	res, text, img = call(t, cs, "screenshot", map[string]any{"machine": "m1", "ocr": true, "ocr_only": true})
	if res.IsError || !(strings.Contains(text, `"lines":[]`) || strings.Contains(text, "OCR failed")) {
		t.Fatalf("ocr: %s", text)
	}
	if strings.Contains(text, `"lines":[]`) && img {
		t.Fatal("ocr_only returned an image")
	}

	recs, _ := al.Query("m1", "", 100)
	if len(recs) < 5 {
		t.Fatalf("audit records: %d", len(recs))
	}
	if recs[len(recs)-1].Thumb == "" {
		t.Error("screenshot call has no audit thumbnail")
	}
}

func TestErrorsAndControlMode(t *testing.T) {
	cs, mgr, _ := setup(t)

	res, text, _ := call(t, cs, "screenshot", map[string]any{"machine": "nope"})
	if !res.IsError || !strings.Contains(text, "unknown machine") {
		t.Fatalf("unknown machine: %s", text)
	}
	res, text, _ = call(t, cs, "screenshot", map[string]any{"machine": "hidden"})
	if !res.IsError || !strings.Contains(text, "disabled") {
		t.Fatalf("disabled machine: %s", text)
	}

	s, _ := mgr.Get("m1")
	s.SetMode(session.ModeHuman)
	res, text, _ = call(t, cs, "mouse", map[string]any{"machine": "m1", "action": "click", "x": 1, "y": 1})
	if !res.IsError || !strings.HasPrefix(text, "BLOCKED:") {
		t.Fatalf("human mode not enforced: %s", text)
	}
	// screenshots stay allowed and say why input is blocked
	res, text, _ = call(t, cs, "screenshot", map[string]any{"machine": "m1"})
	if res.IsError || !strings.Contains(text, `"human"`) {
		t.Fatalf("screenshot in human mode: %s", text)
	}
	s.SetMode(session.ModeAgent)

	res, text, _ = call(t, cs, "run_command", map[string]any{"machine": "m1", "command": "hostname"})
	if !res.IsError || !strings.Contains(text, "enable_drive") {
		t.Fatalf("run_command without drive: %s", text)
	}
	if _, text, _ = call(t, cs, "job", map[string]any{"machine": "m1", "action": "list"}); strings.TrimSpace(text) != "[]" {
		t.Fatalf("job list: %s", text)
	}
	if res, text, _ = call(t, cs, "job", map[string]any{"machine": "m1", "action": "status", "id": "x"}); !res.IsError || !strings.Contains(text, "unknown job") {
		t.Fatalf("job status: %s", text)
	}
}
