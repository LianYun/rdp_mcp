package adminapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image/png"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/LianYun/rdp_mcp/internal/keymap"
	"github.com/LianYun/rdp_mcp/internal/session"
)

// streamMsg is a client -> server message on the live view socket (docs/admin-api.md).
type streamMsg struct {
	Type   string  `json:"type"`   // mouse | key | text | clipboard
	Action string  `json:"action"` // mouse: move | down | up | wheel
	X      float64 `json:"x"`      // frame coordinates
	Y      float64 `json:"y"`
	Button string  `json:"button"`
	DX     int     `json:"dx"`    // wheel notches, positive = right
	DY     int     `json:"dy"`    // wheel notches, positive = down (browser convention)
	Delta  int     `json:"delta"` // deprecated alias of dy
	Code   string  `json:"code"`  // KeyboardEvent.code
	Down   bool    `json:"down"`
	Text   string  `json:"text"`
}

// stream serves GET /machines/{name}/stream: JPEG frames on change, cursor/meta/state
// text messages, and takeover input while the machine is in human mode.
func (a *API) stream(w http.ResponseWriter, r *http.Request) {
	s := a.session(w, r)
	if s == nil {
		return
	}
	q := r.URL.Query()
	fps, _ := strconv.Atoi(q.Get("fps"))
	if fps <= 0 || fps > 30 {
		fps = 10
	}
	maxW, _ := strconv.Atoi(q.Get("max_width"))
	if maxW <= 0 || maxW > 8192 {
		maxW = 1600
	}
	quality, _ := strconv.Atoi(q.Get("quality"))
	if quality <= 0 || quality > 100 {
		quality = 70
	}

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: a.originPatterns()})
	if err != nil {
		return
	}
	c.SetReadLimit(1 << 20)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer c.CloseNow()

	var wmu sync.Mutex
	send := func(typ websocket.MessageType, b []byte) error {
		wmu.Lock()
		defer wmu.Unlock()
		wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
		defer wcancel()
		return c.Write(wctx, typ, b)
	}
	sendJSON := func(v any) error {
		b, _ := json.Marshal(v)
		return send(websocket.MessageText, b)
	}

	frames, unsub := s.SubscribeFrames()
	defer unsub()
	evs, unsubBus := a.Bus.Subscribe(64)
	defer unsubBus()

	_ = sendJSON(map[string]any{"type": "control", "mode": s.Mode()})
	_ = sendJSON(map[string]any{"type": "state", "state": s.State()})
	if q.Get("connect") != "false" && s.State() != session.StateConnected {
		go func() {
			cctx, ccancel := context.WithTimeout(ctx, 90*time.Second)
			defer ccancel()
			if err := s.Connect(cctx); err != nil {
				_ = sendJSON(map[string]any{"type": "error", "message": err.Error()})
			}
		}()
	}

	var scaleMu sync.Mutex
	scale := 1.0 // frame pixels per desktop pixel
	curScale := func() float64 { scaleMu.Lock(); defer scaleMu.Unlock(); return scale }
	go a.streamInput(ctx, s, c, sendJSON, curScale)

	interval := time.Second / time.Duration(fps)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	dirty := true
	var lastW, lastH, lastCurX, lastCurY, lastShape = -1, -1, -1, -1, -1
	lastVisible := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-frames:
			dirty = true
		case ev, ok := <-evs:
			if !ok {
				return
			}
			if fwd := forwardEvent(ev.Type, ev.Data, s.Name()); fwd != nil {
				if err := send(websocket.MessageText, fwd); err != nil {
					return
				}
				if ev.Type == "machine_state" {
					dirty = true
				}
			}
		case <-ticker.C:
			if !dirty && s.State() == session.StateConnected {
				// cursor-only updates
				if err := a.sendCursor(ctx, s, sendJSON, &lastCurX, &lastCurY, &lastShape, &lastVisible, curScale()); err != nil && !errors.Is(err, session.ErrNotConnected) {
					return
				}
				continue
			}
			if !dirty {
				continue
			}
			var shot *session.Shot
			err := s.Peek(ctx, func(o *session.Ops) error {
				var e error
				f := false
				shot, e = o.Screenshot(session.ShotOptions{MaxWidth: maxW, Format: "jpeg", Quality: quality, DrawCursor: &f, KeepScale: true})
				return e
			})
			if errors.Is(err, session.ErrNotConnected) {
				continue
			}
			if err != nil {
				_ = sendJSON(map[string]any{"type": "error", "message": err.Error()})
				continue
			}
			dirty = false
			scaleMu.Lock()
			scale = shot.Scale
			scaleMu.Unlock()
			if shot.Width != lastW || shot.Height != lastH {
				lastW, lastH = shot.Width, shot.Height
				if err := sendJSON(map[string]any{"type": "meta", "width": shot.Width, "height": shot.Height,
					"scale": shot.Scale, "screen": []int{shot.ScreenWidth, shot.ScreenHeight}}); err != nil {
					return
				}
				lastShape = -1 // resend cursor shape after a resize (scale changed)
			}
			if err := send(websocket.MessageBinary, shot.Data); err != nil {
				return
			}
			if err := a.sendCursor(ctx, s, sendJSON, &lastCurX, &lastCurY, &lastShape, &lastVisible, curScale()); err != nil && !errors.Is(err, session.ErrNotConnected) {
				return
			}
		}
	}
}

// sendCursor sends a cursor message when position, visibility or shape changed.
func (a *API) sendCursor(ctx context.Context, s *session.Session, sendJSON func(any) error,
	lx, ly, lshape *int, lvis *bool, sc float64) error {
	return s.Peek(ctx, func(o *session.Ops) error {
		cur := o.CursorOverlay()
		x, y := int(math.Round(float64(cur.X)*sc)), int(math.Round(float64(cur.Y)*sc))
		seq := o.CursorShapeSeq()
		if x == *lx && y == *ly && seq == *lshape && cur.Visible == *lvis {
			return nil
		}
		msg := map[string]any{"type": "cursor", "x": x, "y": y, "visible": cur.Visible}
		if seq != *lshape {
			msg["shape"] = ""
			if cur.Shape != nil {
				var buf bytes.Buffer
				if err := png.Encode(&buf, cur.Shape); err == nil {
					msg["shape"] = base64.StdEncoding.EncodeToString(buf.Bytes())
				}
			}
			msg["hot"] = []int{cur.HotX, cur.HotY}
			if cur.Shape != nil {
				b := cur.Shape.Bounds()
				msg["size"] = []int{b.Dx(), b.Dy()}
			}
		}
		*lx, *ly, *lshape, *lvis = x, y, seq, cur.Visible
		return sendJSON(msg)
	})
}

// forwardEvent converts bus events about this machine into stream text messages.
func forwardEvent(typ string, data json.RawMessage, machine string) []byte {
	var head struct {
		Machine string `json:"machine"`
	}
	if json.Unmarshal(data, &head) != nil || head.Machine != machine {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	switch typ {
	case "control_mode":
		m["type"] = "control"
	case "machine_state":
		m["type"] = "state"
	case "cert_verify":
		m["type"] = "cert_verify"
	default:
		return nil
	}
	b, _ := json.Marshal(m)
	return b
}

// streamInput reads takeover input; it is applied only in human control mode.
func (a *API) streamInput(ctx context.Context, s *session.Session, c *websocket.Conn,
	sendJSON func(any) error, scale func() float64) {
	defer func() { _ = c.CloseNow() }()
	pressed := map[string]bool{} // keys/buttons currently down, released on disconnect
	defer func() {
		if len(pressed) == 0 || s.Mode() != session.ModeHuman {
			return
		}
		rctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Do(rctx, session.SourceHuman, func(o *session.Ops) error {
			for k := range pressed {
				if strings.HasPrefix(k, "btn:") {
					_ = o.Button(strings.TrimPrefix(k, "btn:"), false, o.Session().CursorPos()[0], o.Session().CursorPos()[1])
				} else {
					_ = o.SendDOMKey(k, false)
				}
			}
			return nil
		})
	}()
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		var m streamMsg
		if err := json.Unmarshal(data, &m); err != nil {
			_ = sendJSON(map[string]any{"type": "error", "message": "bad message: " + err.Error()})
			continue
		}
		if s.Mode() != session.ModeHuman {
			_ = sendJSON(map[string]any{"type": "error", "message": "input ignored: take over the machine first"})
			continue
		}
		err = s.Do(ctx, session.SourceHuman, func(o *session.Ops) error {
			sc := scale()
			if sc <= 0 {
				sc = 1
			}
			w, h := o.ScreenSize()
			x := clampInt(int(math.Round(m.X/sc)), 0, w-1)
			y := clampInt(int(math.Round(m.Y/sc)), 0, h-1)
			switch m.Type {
			case "mouse":
				switch m.Action {
				case "move":
					return o.Move(x, y)
				case "down":
					pressed["btn:"+m.Button] = true
					return o.Button(m.Button, true, x, y)
				case "up":
					delete(pressed, "btn:"+m.Button)
					return o.Button(m.Button, false, x, y)
				case "wheel":
					dy := m.DY
					if dy == 0 {
						dy = m.Delta
					}
					if dy != 0 {
						if err := o.Wheel(x, y, -dy); err != nil {
							return err
						}
					}
					if m.DX != 0 {
						return o.WheelH(x, y, m.DX)
					}
					return nil
				}
				return errors.New("unknown mouse action " + m.Action)
			case "key":
				if _, ok := keymap.FromDOMCode(m.Code); !ok {
					return errors.New("unsupported key " + m.Code)
				}
				if m.Down {
					pressed[m.Code] = true
				} else {
					delete(pressed, m.Code)
				}
				return o.SendDOMKey(m.Code, m.Down)
			case "text":
				return o.Type(m.Text)
			case "clipboard":
				return o.ClipboardSet(m.Text)
			}
			return errors.New("unknown message type " + m.Type)
		})
		if err != nil {
			_ = sendJSON(map[string]any{"type": "error", "message": err.Error()})
		}
	}
}

func clampInt(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return max(lo, min(v, hi))
}

// originPatterns lists the browser origins allowed to open WebSockets.
func (a *API) originPatterns() []string {
	p := []string{"localhost", "localhost:*", "127.0.0.1:*", "tauri.localhost"}
	for _, o := range a.Origins {
		o = strings.TrimPrefix(strings.TrimPrefix(o, "https://"), "http://")
		p = append(p, o)
	}
	return p
}
