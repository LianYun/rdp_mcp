package vnc

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"
)

// Worker IPC (docs/worker-ipc.md, "VNC worker"): the same protocol as the C remoter-worker,
// so the service's session layer drives both the same way.

const ipcProtocol = 1

// RDP pointer flags received in "mouse" / "mouse_ex" requests (MS-RDPBCGR 2.2.8.1.1.3.1.1.3).
const (
	ptrHWheel   = 0x0400
	ptrWheel    = 0x0200
	ptrWheelNeg = 0x0100
	ptrMove     = 0x0800
	ptrDown     = 0x8000
	ptrButton1  = 0x1000
	ptrButton2  = 0x2000
	ptrButton3  = 0x4000
	ptrXButton1 = 0x0001
	ptrXButton2 = 0x0002
)

// RFB button mask bits.
const (
	btnLeft   = 1
	btnMiddle = 2
	btnRight  = 4
	btnUp     = 8
	btnDown   = 16
	btnLeftW  = 32
	btnRightW = 64
	btnBack   = 128
)

type ipcWorker struct {
	conn net.Conn
	wmu  sync.Mutex
	log  *log.Logger

	mu         sync.Mutex
	vnc        *Client
	subscribed bool
	lastDirty  time.Time
	dirtyTimer *time.Timer
}

// RunWorker connects to the service's IPC socket and serves requests until the IPC
// connection or the VNC connection ends.
func RunWorker(sock string, version string) error {
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return fmt.Errorf("connect ipc %s: %w", sock, err)
	}
	w := &ipcWorker{conn: conn, log: log.New(os.Stderr, "vnc-worker: ", log.LstdFlags)}
	defer w.shutdown()
	if err := w.send(map[string]any{"event": "hello", "version": version, "freerdp": "", "backend": "vnc", "protocol": ipcProtocol}); err != nil {
		return err
	}
	r := bufio.NewReaderSize(conn, 1<<16)
	var hdr [4]byte
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		n := binary.BigEndian.Uint32(hdr[:])
		if n == 0 || n > 64<<20 {
			return fmt.Errorf("bad ipc frame length %d", n)
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			return err
		}
		if buf[0] != 'J' {
			continue
		}
		var req map[string]any
		if err := json.Unmarshal(buf[1:], &req); err != nil {
			w.log.Printf("bad request: %v", err)
			continue
		}
		if stop := w.handle(req); stop {
			return nil
		}
	}
}

func (w *ipcWorker) shutdown() {
	w.mu.Lock()
	c := w.vnc
	w.mu.Unlock()
	if c != nil {
		c.Close()
	}
	_ = w.conn.Close()
}

func (w *ipcWorker) writeFrame(typ byte, payload []byte) error {
	hdr := make([]byte, 5)
	binary.BigEndian.PutUint32(hdr, uint32(len(payload)+1))
	hdr[4] = typ
	w.wmu.Lock()
	defer w.wmu.Unlock()
	if _, err := w.conn.Write(hdr); err != nil {
		return err
	}
	_, err := w.conn.Write(payload)
	return err
}

func (w *ipcWorker) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return w.writeFrame('J', b)
}

func (w *ipcWorker) reply(id int64, ok bool, fields map[string]any) {
	m := map[string]any{"id": id, "ok": ok}
	for k, v := range fields {
		m[k] = v
	}
	if err := w.send(m); err != nil {
		w.log.Printf("ipc write: %v", err)
	}
}

func (w *ipcWorker) fail(id int64, err error) {
	w.reply(id, false, map[string]any{"error": err.Error()})
}

func num(req map[string]any, k string) int {
	if f, ok := req[k].(float64); ok {
		return int(f)
	}
	return 0
}

func str(req map[string]any, k string) string {
	s, _ := req[k].(string)
	return s
}

func boolean(req map[string]any, k string) bool {
	switch v := req[k].(type) {
	case bool:
		return v
	case float64:
		return v != 0
	}
	return false
}

// handle serves one request; it returns true when the worker should exit.
func (w *ipcWorker) handle(req map[string]any) bool {
	id := int64(num(req, "id"))
	op := str(req, "op")
	if op == "connect" {
		return w.connect(id, req)
	}
	if op == "disconnect" {
		w.reply(id, true, nil)
		return true
	}
	w.mu.Lock()
	c := w.vnc
	w.mu.Unlock()
	if c == nil {
		w.fail(id, errors.New("not connected"))
		return false
	}
	switch op {
	case "status":
		wd, ht := c.Size()
		seq, idle, since := c.Stats()
		idleMs := -1
		if idle >= 0 {
			idleMs = int(idle / time.Millisecond)
		}
		w.reply(id, true, map[string]any{"connected": true, "width": wd, "height": ht, "dirty_seq": seq,
			"idle_ms": idleMs, "logon_count": 1, "logon_ms_ago": int(since / time.Millisecond)})
	case "capture":
		wd, ht, pix, seq := c.Snapshot()
		hdr := make([]byte, 16, 16+len(pix))
		binary.BigEndian.PutUint32(hdr[0:], uint32(wd))
		binary.BigEndian.PutUint32(hdr[4:], uint32(ht))
		binary.BigEndian.PutUint32(hdr[8:], uint32(wd*4))
		binary.BigEndian.PutUint32(hdr[12:], uint32(id))
		if err := w.writeFrame('F', append(hdr, pix...)); err != nil {
			return true
		}
		w.reply(id, true, map[string]any{"width": wd, "height": ht, "dirty_seq": seq})
	case "cursor_get":
		_, x, y := c.PointerState()
		w.reply(id, true, map[string]any{"x": x, "y": y, "visible": true, "hot_x": 0, "hot_y": 0,
			"width": 0, "height": 0, "shape_seq": 0, "has_shape": false})
	case "subscribe":
		w.mu.Lock()
		w.subscribed = boolean(req, "on")
		w.mu.Unlock()
		w.reply(id, true, nil)
	case "mouse":
		if err := w.mouse(c, uint16(num(req, "flags")), num(req, "x"), num(req, "y")); err != nil {
			w.fail(id, err)
			return false
		}
		w.reply(id, true, nil)
	case "mouse_ex":
		if err := w.mouseEx(c, uint16(num(req, "flags")), num(req, "x"), num(req, "y")); err != nil {
			w.fail(id, err)
			return false
		}
		w.reply(id, true, nil)
	case "key":
		sym, ok := KeysymForScancode(uint8(num(req, "scancode")), boolean(req, "extended"))
		if !ok {
			w.fail(id, fmt.Errorf("key with scancode 0x%x is not available over VNC", num(req, "scancode")))
			return false
		}
		if err := c.Key(sym, boolean(req, "down")); err != nil {
			w.fail(id, err)
			return false
		}
		w.reply(id, true, nil)
	case "unicode":
		for _, r := range str(req, "text") {
			sym, ok := KeysymForRune(r)
			if !ok {
				continue
			}
			if err := c.Key(sym, true); err != nil {
				w.fail(id, err)
				return false
			}
			if err := c.Key(sym, false); err != nil {
				w.fail(id, err)
				return false
			}
			time.Sleep(4 * time.Millisecond)
		}
		w.reply(id, true, nil)
	case "clipboard_get", "clipboard_set":
		w.fail(id, errors.New("clipboard is not available over VNC (the service uses SSH for macOS clipboards)"))
	case "resize":
		w.fail(id, errors.New("changing the resolution is not supported for macOS (VNC) machines"))
	case "cert_reply":
		w.reply(id, true, nil)
	default:
		w.fail(id, fmt.Errorf("unknown op %q", op))
	}
	return false
}

func (w *ipcWorker) connect(id int64, req map[string]any) bool {
	w.mu.Lock()
	already := w.vnc != nil
	w.mu.Unlock()
	if already {
		w.fail(id, errors.New("already connected"))
		return false
	}
	timeout := time.Duration(num(req, "connect_timeout_ms")) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	port := num(req, "port")
	if port == 0 {
		port = 5900
	}
	c, err := Dial(context.Background(), Options{
		Host: str(req, "host"), Port: port, Username: str(req, "username"), Password: str(req, "password"), Timeout: timeout,
	})
	if err != nil {
		w.fail(id, err)
		return true // the service starts a new worker for the next attempt
	}
	c.OnUpdate = w.frameDirty
	c.OnResize = func(wd, ht int) {
		_ = w.send(map[string]any{"event": "resized", "width": wd, "height": ht})
	}
	w.mu.Lock()
	w.vnc = c
	w.mu.Unlock()
	go func() {
		<-c.Done()
		reason := "connection closed"
		if err := c.Err(); err != nil {
			reason = err.Error()
		}
		_ = w.send(map[string]any{"event": "disconnected", "code": 0, "reason": reason})
		// Closing the IPC connection makes RunWorker return and the process exit.
		time.Sleep(100 * time.Millisecond)
		_ = w.conn.Close()
	}()
	wd, ht := c.Size()
	w.log.Printf("connected to %s (RFB %s, %q, %dx%d)", str(req, "host"), c.Version, c.Name, wd, ht)
	w.reply(id, true, map[string]any{"width": wd, "height": ht})
	return false
}

// frameDirty sends throttled frame_dirty events (<=30/s) while subscribed.
func (w *ipcWorker) frameDirty() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.subscribed || w.dirtyTimer != nil {
		return
	}
	const minGap = 33 * time.Millisecond
	wait := minGap - time.Since(w.lastDirty)
	if wait <= 0 {
		w.lastDirty = time.Now()
		go w.emitDirty()
		return
	}
	w.dirtyTimer = time.AfterFunc(wait, func() {
		w.mu.Lock()
		w.dirtyTimer = nil
		w.lastDirty = time.Now()
		w.mu.Unlock()
		w.emitDirty()
	})
}

func (w *ipcWorker) emitDirty() {
	w.mu.Lock()
	c := w.vnc
	w.mu.Unlock()
	if c == nil {
		return
	}
	seq, _, _ := c.Stats()
	_ = w.send(map[string]any{"event": "frame_dirty", "dirty_seq": seq})
}

func (w *ipcWorker) cursorEvent(c *Client) {
	_, x, y := c.PointerState()
	_ = w.send(map[string]any{"event": "cursor", "x": x, "y": y, "visible": true, "shape_seq": 0})
}

// mouse translates RDP pointer flags into RFB pointer events.
func (w *ipcWorker) mouse(c *Client, flags uint16, x, y int) error {
	buttons, _, _ := c.PointerState()
	if flags&(ptrWheel|ptrHWheel) != 0 {
		neg := flags&ptrWheelNeg != 0
		var b byte
		switch {
		case flags&ptrWheel != 0 && !neg:
			b = btnUp
		case flags&ptrWheel != 0:
			b = btnDown
		case neg:
			b = btnLeftW
		default:
			b = btnRightW
		}
		if err := c.Pointer(buttons|b, x, y); err != nil {
			return err
		}
		return c.Pointer(buttons, x, y)
	}
	var bit byte
	switch {
	case flags&ptrButton1 != 0:
		bit = btnLeft
	case flags&ptrButton2 != 0:
		bit = btnRight
	case flags&ptrButton3 != 0:
		bit = btnMiddle
	}
	if bit != 0 {
		if flags&ptrDown != 0 {
			buttons |= bit
		} else {
			buttons &^= bit
		}
	}
	if err := c.Pointer(buttons, x, y); err != nil {
		return err
	}
	w.cursorEvent(c)
	return nil
}

func (w *ipcWorker) mouseEx(c *Client, flags uint16, x, y int) error {
	if flags&ptrXButton2 != 0 {
		return errors.New("the x2 (forward) mouse button is not available over VNC")
	}
	if flags&ptrXButton1 == 0 {
		return w.mouse(c, ptrMove, x, y)
	}
	buttons, _, _ := c.PointerState()
	if flags&ptrDown != 0 {
		buttons |= btnBack
	} else {
		buttons &^= btnBack
	}
	return c.Pointer(buttons, x, y)
}
