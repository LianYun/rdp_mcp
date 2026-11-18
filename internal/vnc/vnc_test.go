package vnc

import (
	"context"
	"testing"
	"time"

	"github.com/LianYun/rdp_mcp/internal/vnc/vnctest"
)

func dialTest(t *testing.T, srv *vnctest.Server, user, pass string) *Client {
	t.Helper()
	c, err := Dial(context.Background(), Options{Host: "127.0.0.1", Port: srv.Port(), Username: user, Password: pass, Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}

func checkPattern(t *testing.T, c *Client) {
	t.Helper()
	w, h, pix, seq := c.Snapshot()
	if seq < 1 {
		t.Fatalf("dirty_seq = %d", seq)
	}
	want := vnctest.Pattern(w, h)
	for i := 0; i < w*h; i++ {
		if pix[i*4] != want[i*3+2] || pix[i*4+1] != want[i*3+1] || pix[i*4+2] != want[i*3] {
			t.Fatalf("pixel (%d,%d) = %v, want rgb %v", i%w, i/w, pix[i*4:i*4+3], want[i*3:i*3+3])
		}
	}
}

func TestARDAuthAndZRLE(t *testing.T) {
	// 330x150: partial tiles on both axes.
	srv := vnctest.Start(t, vnctest.Options{Width: 330, Height: 150, User: "alice", Pass: "s3cret pass"})
	c := dialTest(t, srv, "alice", "s3cret pass")
	if w, h := c.Size(); w != 330 || h != 150 {
		t.Fatalf("size %dx%d", w, h)
	}
	if c.Version != "3.889" {
		t.Errorf("version %q", c.Version)
	}
	checkPattern(t, c)

	// An incremental update after a change goes through the same zlib stream.
	srv.SetPixel(5, 5, 1, 2, 3)
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, _, pix, seq := c.Snapshot()
		i := (5*330 + 5) * 4
		if seq >= 2 && pix[i] == 3 && pix[i+1] == 2 && pix[i+2] == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("incremental update not applied (seq %d)", seq)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestARDAuthFailure(t *testing.T) {
	srv := vnctest.Start(t, vnctest.Options{User: "alice", Pass: "right"})
	_, err := Dial(context.Background(), Options{Host: "127.0.0.1", Port: srv.Port(), Username: "alice", Password: "wrong", Timeout: 5 * time.Second})
	if _, ok := err.(*AuthError); !ok {
		t.Fatalf("want AuthError, got %v", err)
	}
}

func TestVNCPasswordAuth(t *testing.T) {
	srv := vnctest.Start(t, vnctest.Options{Security: 2, Pass: "pw123"})
	c := dialTest(t, srv, "", "pw123")
	checkPattern(t, c)
}

func TestNoAuthAndInput(t *testing.T) {
	srv := vnctest.Start(t, vnctest.Options{Security: 1})
	c := dialTest(t, srv, "", "")
	if err := c.Pointer(btnLeft, 10, 20); err != nil {
		t.Fatal(err)
	}
	if err := c.Pointer(0, 5000, 20); err != nil { // clamped to the screen
		t.Fatal(err)
	}
	sym, _ := KeysymForScancode(0x5b, true)
	c.Key(sym, true)
	c.Key(sym, false)
	time.Sleep(200 * time.Millisecond)
	keys, ptrs := srv.Events()
	if len(ptrs) != 2 || ptrs[0] != (vnctest.PointerEvent{Buttons: 1, X: 10, Y: 20}) || ptrs[1].X != 319 {
		t.Fatalf("pointer events %+v", ptrs)
	}
	if len(keys) != 2 || keys[0] != (vnctest.KeyEvent{Sym: 0xffeb, Down: true}) || keys[1].Down {
		t.Fatalf("key events %+v", keys)
	}
}

func TestKeysyms(t *testing.T) {
	cases := []struct {
		code uint8
		ext  bool
		want uint32
	}{
		{0x1e, false, 'a'}, {0x2c, false, 'z'}, {0x02, false, '1'}, {0x0b, false, '0'},
		{0x1c, false, 0xff0d}, {0x5b, true, 0xffeb}, {0x38, false, 0xffe9}, {0x1d, false, 0xffe3},
		{0x4b, true, 0xff51}, {0x53, true, 0xffff}, {0x3b, false, 0xffbe}, {0x58, false, 0xffc9},
	}
	for _, c := range cases {
		if got, ok := KeysymForScancode(c.code, c.ext); !ok || got != c.want {
			t.Errorf("scancode %#x ext=%v: got %#x, want %#x", c.code, c.ext, got, c.want)
		}
	}
	for r, want := range map[rune]uint32{'A': 'A', 'é': 0xe9, '中': 0x01004e2d, '\n': 0xff0d, '\t': 0xff09} {
		if got, ok := KeysymForRune(r); !ok || got != want {
			t.Errorf("rune %q: got %#x, want %#x", r, got, want)
		}
	}
	if _, ok := KeysymForRune('\r'); ok {
		t.Error("\\r should be skipped")
	}
}
