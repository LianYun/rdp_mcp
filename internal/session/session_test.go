package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LianYun/rdp_mcp/internal/config"
	"github.com/LianYun/rdp_mcp/internal/events"
	"github.com/LianYun/rdp_mcp/internal/testutil/fakeworker"
)

func TestMain(m *testing.M) {
	fakeworker.MaybeRun()
	os.Exit(m.Run())
}

// newFakeManager returns a manager whose workers are the fake worker.
func newFakeManager(t *testing.T, yaml string) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "input.jsonl")
	for _, kv := range fakeworker.Env(logPath) {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	cfg, err := config.Parse([]byte(yaml), filepath.Join(dir, "configs.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	m := NewManager(cfg, Options{WorkerPath: exe, DataDir: dir, Bus: events.NewBus()})
	t.Cleanup(m.Shutdown)
	return m, logPath
}

const oneMachine = `
defaults:
  width: 1920
  height: 1080
  enable_drive: false
machines:
  - name: m1
    host: 127.0.0.1
    username: u
    password: p
`

func inputs(t *testing.T, path string) []map[string]any {
	t.Helper()
	b, _ := os.ReadFile(path)
	var out []map[string]any
	for _, ln := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if ln == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err == nil {
			out = append(out, m)
		}
	}
	return out
}

func TestConnectScreenshotAndCoordinates(t *testing.T) {
	m, logPath := newFakeManager(t, oneMachine)
	s, err := m.Get("m1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var shot *Shot
	err = s.Do(ctx, SourceAgent, func(o *Ops) error {
		var e error
		shot, e = o.Screenshot(ShotOptions{})
		if e != nil {
			return e
		}
		// (640,360) in a 1280-wide screenshot of a 1920x1080 desktop = (960,540)
		x, y := o.ToReal(640, 360)
		return o.Click(x, y, "left", 1, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	if shot.Width != 1280 || shot.ScreenWidth != 1920 || s.Info().State != StateConnected {
		t.Fatalf("shot %+v state %s", shot, s.Info().State)
	}
	var sawDown bool
	for _, in := range inputs(t, logPath) {
		if in["op"] == "mouse" && int(in["flags"].(float64))&0x8000 != 0 {
			sawDown = true
			if in["x"].(float64) != 960 || in["y"].(float64) != 540 {
				t.Fatalf("click at %v,%v, want 960,540", in["x"], in["y"])
			}
		}
	}
	if !sawDown {
		t.Fatal("no button-down event recorded")
	}
}

func TestControlModeBlocksAgent(t *testing.T) {
	m, _ := newFakeManager(t, oneMachine)
	s, _ := m.Get("m1")
	s.SetMode(ModePaused)
	err := s.Do(context.Background(), SourceAgent, func(o *Ops) error { return nil })
	var ce *ControlError
	if !errors.As(err, &ce) || ce.Mode != ModePaused {
		t.Fatalf("want ControlError(paused), got %v", err)
	}
	s.SetMode(ModeHuman)
	if err := s.Do(context.Background(), SourceAgent, func(o *Ops) error { return nil }); err == nil {
		t.Fatal("agent allowed in human mode")
	}
	m.EmergencyStop()
	if s.Mode() != ModePaused {
		t.Fatal("emergency stop did not pause")
	}
}

func TestConnectFailure(t *testing.T) {
	m, _ := newFakeManager(t, strings.Replace(oneMachine, "127.0.0.1", "fail.invalid", 1))
	s, _ := m.Get("m1")
	err := s.Connect(context.Background())
	if err == nil || s.Info().State != StateFailed || !strings.Contains(s.Info().LastError, "simulated") {
		t.Fatalf("err=%v info=%+v", err, s.Info())
	}
}

func TestReconnectAfterCrash(t *testing.T) {
	m, _ := newFakeManager(t, oneMachine)
	s, _ := m.Get("m1")
	ctx := context.Background()
	if err := s.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	// Crash well after the quick-drop window so the regular reconnect loop runs.
	_ = s.Peek(ctx, func(o *Ops) error { _, _ = o.call("crash", nil); return nil })
	deadline := time.Now().Add(20 * time.Second)
	sawReconnecting := false
	for time.Now().Before(deadline) {
		st := s.State()
		if st == StateReconnecting {
			sawReconnecting = true
		}
		if sawReconnecting && st == StateConnected {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("did not reconnect (saw reconnecting=%v, state=%s)", sawReconnecting, s.State())
}

func TestResizeAndClipboard(t *testing.T) {
	m, _ := newFakeManager(t, oneMachine)
	s, _ := m.Get("m1")
	err := s.Do(context.Background(), SourceAgent, func(o *Ops) error {
		w, h, err := o.Resize(1600, 900)
		if err != nil || w != 1600 || h != 900 {
			t.Errorf("resize: %d %d %v", w, h, err)
		}
		if err := o.ClipboardSet("你好"); err != nil {
			return err
		}
		got, err := o.ClipboardGet()
		if got != "你好" {
			t.Errorf("clipboard %q", got)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestIdleHold(t *testing.T) {
	m, _ := newFakeManager(t, strings.Replace(oneMachine, "enable_drive: false", "enable_drive: false\n  idle_disconnect: 1ms", 1))
	s, _ := m.Get("m1")
	if err := s.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	release := s.HoldIdle()
	time.Sleep(5 * time.Millisecond)
	s.reapIfIdle()
	if s.State() != StateConnected {
		t.Fatal("reaped while held")
	}
	release()
	s.reapIfIdle()
	if s.State() != StateIdle {
		t.Fatalf("not reaped after release: %s", s.State())
	}
}
