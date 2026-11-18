package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/LianYun/rdp_mcp/internal/config"
	"github.com/LianYun/rdp_mcp/internal/events"
	"github.com/LianYun/rdp_mcp/internal/testutil/vncworker"
	"github.com/LianYun/rdp_mcp/internal/vnc/vnctest"
)

func TestMacSessionOverVNC(t *testing.T) {
	srv := vnctest.Start(t, vnctest.Options{User: "alice", Pass: "pw"})
	t.Setenv(vncworker.Env, "1")
	dir := t.TempDir()
	yaml := fmt.Sprintf("machines:\n  - name: mac\n    protocol: vnc\n    host: 127.0.0.1\n    port: %d\n    username: alice\n    password: pw\n    enable_ssh: false\n", srv.Port())
	cfg, err := config.Parse([]byte(yaml), filepath.Join(dir, "c.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	m := NewManager(cfg, Options{DataDir: dir, Bus: events.NewBus(), VNCWorkerPath: exe})
	t.Cleanup(m.Shutdown)
	s, _ := m.Get("mac")
	if in := s.Info(); in.Platform != "macos" || in.Protocol != "vnc" || in.Port != srv.Port() {
		t.Fatalf("info %+v", in)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err = s.Do(ctx, SourceAgent, func(o *Ops) error {
		shot, err := o.Screenshot(ShotOptions{Format: "png"})
		if err != nil {
			return err
		}
		if shot.ScreenWidth != 320 || shot.ScreenHeight != 200 || shot.Scale != 1 {
			return fmt.Errorf("shot %+v", shot)
		}
		want := vnctest.Pattern(320, 200)
		r, g, b, _ := shot.Source.At(300, 150).RGBA()
		i := (150*320 + 300) * 3
		if byte(r>>8) != want[i] || byte(g>>8) != want[i+1] || byte(b>>8) != want[i+2] {
			return fmt.Errorf("pixel mismatch")
		}
		if err := o.Click(10, 20, "", 1, nil); err != nil {
			return err
		}
		if err := o.Hotkey([]string{"cmd", "c"}); err != nil {
			return err
		}
		if err := o.Type("aB中\n"); err != nil { // no SSH: key events
			return err
		}
		if err := o.Scroll(5, 5, "down", 1); err != nil {
			return err
		}
		if _, _, err := o.Resize(800, 600); err == nil {
			return fmt.Errorf("resize should fail on macOS")
		}
		if _, err := o.ClipboardGet(); err == nil {
			return fmt.Errorf("clipboard without SSH should fail")
		}
		if c := s.CursorPos(); c != [2]int{5, 5} {
			return fmt.Errorf("cursor %v", c)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	keys, ptrs := srv.Events()
	var syms []uint32
	for _, k := range keys {
		if k.Down {
			syms = append(syms, k.Sym)
		}
	}
	if want := []uint32{0xffeb, 'c', 'a', 'B', 0x01004e2d, 0xff0d}; !slices.Equal(syms, want) {
		t.Fatalf("key downs %#x, want %#x", syms, want)
	}
	var clicks, wheel int
	for _, p := range ptrs {
		if p.Buttons == 1 && p.X == 10 && p.Y == 20 {
			clicks++
		}
		if p.Buttons == 16 {
			wheel++
		}
	}
	if clicks != 1 || wheel != 1 {
		t.Fatalf("pointer events %+v", ptrs)
	}
}
