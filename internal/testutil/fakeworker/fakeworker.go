// Package fakeworker is a stand-in for rdp-worker used by tests. It speaks the IPC
// protocol (docs/worker-ipc.md) against a simulated 1280x800 desktop and logs every
// input request as a JSON line to $FAKE_WORKER_LOG.
//
// Use it from a test package:
//
//	func TestMain(m *testing.M) { fakeworker.MaybeRun(); os.Exit(m.Run()) }
//
// and point session.Options.WorkerPath at os.Args[0] with FAKE_WORKER=1 in the env
// (see Env).
package fakeworker

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"sync"
)

const envFlag = "FAKE_WORKER"

// Env returns the environment needed to run the current test binary as a fake worker.
func Env(logPath string) []string {
	return []string{envFlag + "=1", "FAKE_WORKER_LOG=" + logPath}
}

// MaybeRun runs the fake worker and exits if the process was started as one.
func MaybeRun() {
	if os.Getenv(envFlag) != "1" {
		return
	}
	var sock string
	for i, a := range os.Args {
		if a == "--ipc" && i+1 < len(os.Args) {
			sock = os.Args[i+1]
		}
	}
	os.Exit(run(sock))
}

type state struct {
	conn      net.Conn
	wmu       sync.Mutex
	log       *os.File
	w, h      int
	clipboard string
	dirty     int
	curX      int
	curY      int
	down      map[int]bool // pressed scancodes (extended keys | 0x100)
	runDialog bool         // simulated Run dialog visible (Win+R opens, Enter/Esc closes)
}

// NoRunDialog makes Win+R do nothing (simulates a locked session) when set in the env.
const envNoRunDialog = "FAKE_WORKER_NO_RUN_DIALOG"

func run(sock string) int {
	c, err := net.Dial("unix", sock)
	if err != nil {
		return 1
	}
	st := &state{conn: c, w: 1280, h: 800, down: map[int]bool{}}
	if p := os.Getenv("FAKE_WORKER_LOG"); p != "" {
		st.log, _ = os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	}
	st.send(map[string]any{"event": "hello", "version": "fake", "freerdp": "fake", "protocol": 1})
	r := bufio.NewReader(c)
	var hdr [4]byte
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return 0
		}
		buf := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		if _, err := io.ReadFull(r, buf); err != nil {
			return 0
		}
		if len(buf) == 0 || buf[0] != 'J' {
			continue
		}
		var req map[string]any
		if json.Unmarshal(buf[1:], &req) != nil {
			continue
		}
		if !st.handle(req) {
			return 0
		}
	}
}

func (st *state) frame(typ byte, payload []byte) {
	st.wmu.Lock()
	defer st.wmu.Unlock()
	var hdr [5]byte
	binary.BigEndian.PutUint32(hdr[:4], uint32(len(payload)+1))
	hdr[4] = typ
	_, _ = st.conn.Write(append(hdr[:], payload...))
}

func (st *state) send(v map[string]any) {
	b, _ := json.Marshal(v)
	st.frame('J', b)
}

func (st *state) reply(id any, extra map[string]any) {
	m := map[string]any{"id": id, "ok": true}
	for k, v := range extra {
		m[k] = v
	}
	st.send(m)
}

func (st *state) fail(id any, msg string) {
	st.send(map[string]any{"id": id, "ok": false, "error": msg})
}

func (st *state) record(req map[string]any) {
	if st.log == nil {
		return
	}
	b, _ := json.Marshal(req)
	_, _ = st.log.Write(append(b, '\n'))
}

func num(v any) int {
	f, _ := v.(float64)
	return int(f)
}

// handle answers one request; returns false when the worker should exit.
func (st *state) handle(req map[string]any) bool {
	id := req["id"]
	op, _ := req["op"].(string)
	switch op {
	case "connect":
		if req["host"] == "fail.invalid" {
			st.fail(id, "connect failed: simulated failure")
			return false
		}
		if w := num(req["width"]); w > 0 {
			st.w, st.h = w, num(req["height"])
		}
		st.dirty = 1
		st.reply(id, map[string]any{"width": st.w, "height": st.h})
	case "disconnect":
		st.reply(id, nil)
		return false
	case "status":
		st.reply(id, map[string]any{"connected": true, "width": st.w, "height": st.h, "dirty_seq": st.dirty,
			"idle_ms": 5000, "logon_count": 1, "logon_ms_ago": 5000})
	case "capture":
		pix := make([]byte, 16+st.w*st.h*4)
		binary.BigEndian.PutUint32(pix[0:], uint32(st.w))
		binary.BigEndian.PutUint32(pix[4:], uint32(st.h))
		binary.BigEndian.PutUint32(pix[8:], uint32(st.w*4))
		binary.BigEndian.PutUint32(pix[12:], uint32(num(id)))
		for i := 16; i < len(pix); i += 4 {
			pix[i], pix[i+1], pix[i+2], pix[i+3] = 0x40, 0x80, 0xC0, 0xFF
		}
		if st.runDialog { // a light 400x200 box at the bottom-left
			for y := st.h - 260; y < st.h-60; y++ {
				for x := 10; x < 410; x++ {
					o := 16 + (y*st.w+x)*4
					pix[o], pix[o+1], pix[o+2] = 0xF0, 0xF0, 0xF0
				}
			}
		}
		st.frame('F', pix)
		st.reply(id, map[string]any{"width": st.w, "height": st.h, "dirty_seq": st.dirty})
	case "cursor_get":
		st.reply(id, map[string]any{"x": st.curX, "y": st.curY, "visible": true, "shape_seq": 0, "has_shape": false})
	case "subscribe", "cert_reply":
		st.reply(id, nil)
	case "mouse", "mouse_ex":
		st.record(req)
		st.curX, st.curY = num(req["x"]), num(req["y"])
		st.dirty++
		st.reply(id, nil)
	case "key", "unicode":
		st.record(req)
		st.dirty++
		if op == "key" {
			code := num(req["scancode"])
			if req["extended"] == true {
				code |= 0x100
			}
			isDown := req["down"] == true
			if isDown && code == 0x13 && (st.down[0x15B] || st.down[0x15C]) && os.Getenv(envNoRunDialog) == "" {
				st.runDialog = true // Win+R
			}
			if isDown && (code == 0x1C || code == 0x01) {
				st.runDialog = false // Enter / Esc
			}
			st.down[code] = isDown
		}
		st.reply(id, nil)
	case "clipboard_set":
		st.clipboard, _ = req["text"].(string)
		st.reply(id, nil)
	case "clipboard_get":
		st.reply(id, map[string]any{"text": st.clipboard})
	case "resize":
		st.record(req)
		st.w, st.h = num(req["width"])&^1, num(req["height"])
		st.reply(id, nil)
		st.send(map[string]any{"event": "resized", "width": st.w, "height": st.h})
	case "crash":
		os.Exit(3)
	default:
		st.fail(id, "unknown op "+op)
	}
	return true
}
