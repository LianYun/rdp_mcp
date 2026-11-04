package session

import (
	"context"
	"fmt"
	"image"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LianYun/rdp_mcp/internal/imaging"
	"github.com/LianYun/rdp_mcp/internal/keymap"
	"github.com/LianYun/rdp_mcp/internal/worker"
)

// Ops are operations on a connected session, valid only inside Session.Do.
type Ops struct {
	s   *Session
	w   *worker.Client
	ctx context.Context
}

func (o *Ops) call(op string, p map[string]any) (worker.Reply, error) {
	ctx, cancel := context.WithTimeout(o.ctx, 30*time.Second)
	defer cancel()
	return o.w.Call(ctx, op, p)
}

func (o *Ops) Session() *Session { return o.s }

// ScreenSize returns the remote desktop size.
func (o *Ops) ScreenSize() (int, int) {
	o.s.mu.Lock()
	defer o.s.mu.Unlock()
	return o.s.width, o.s.height
}

// Sleep waits d or until ctx is done.
func (o *Ops) Sleep(d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-o.ctx.Done():
		return o.ctx.Err()
	}
}

// ---- coordinates ----

// DefaultScale is the scale used when the agent has not taken a screenshot yet.
func (o *Ops) scale() float64 {
	o.s.mu.Lock()
	sc, w, h := o.s.lastScale, o.s.width, o.s.height
	o.s.mu.Unlock()
	if sc > 0 {
		return sc
	}
	return imaging.Scale(w, h, o.s.m.Config().Server.EffectiveMaxWidth())
}

// ToReal converts screenshot coordinates to desktop coordinates.
func (o *Ops) ToReal(x, y int) (int, int) {
	sc := o.scale()
	w, h := o.ScreenSize()
	rx := int(math.Round(float64(x) / sc))
	ry := int(math.Round(float64(y) / sc))
	return clamp(rx, 0, w-1), clamp(ry, 0, h-1)
}

// ToShot converts desktop coordinates to screenshot coordinates.
func (o *Ops) ToShot(x, y int) (int, int) {
	sc := o.scale()
	return int(math.Round(float64(x) * sc)), int(math.Round(float64(y) * sc))
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return max(lo, min(v, hi))
}

// ---- screenshots ----

type ShotOptions struct {
	MaxWidth   int              // long side; 0 = configured default
	Region     *image.Rectangle // in screenshot coordinates
	Format     string           // png|jpeg
	DrawCursor *bool
	// KeepScale keeps the agent coordinate system unchanged (UI/admin captures).
	KeepScale bool
	// Quality is the JPEG quality (default 85).
	Quality int
}

type Shot struct {
	Data         []byte      `json:"-"`
	MIME         string      `json:"mime"`
	Width        int         `json:"width"`
	Height       int         `json:"height"`
	ScreenWidth  int         `json:"screen_width"`
	ScreenHeight int         `json:"screen_height"`
	Scale        float64     `json:"scale"`
	Region       []int       `json:"region,omitempty"`
	Zoom         float64     `json:"zoom,omitempty"` // image pixels per screenshot coordinate (region only)
	CursorX      int         `json:"cursor_x"`
	CursorY      int         `json:"cursor_y"`
	DirtySeq     int         `json:"-"`
	Image        image.Image `json:"-"`
}

// CaptureRGBA grabs the full-resolution framebuffer.
func (o *Ops) CaptureRGBA(drawCursor bool) (*image.RGBA, imaging.Cursor, error) {
	r, err := o.call("capture", nil)
	if err != nil {
		return nil, imaging.Cursor{}, err
	}
	if r.Frame == nil {
		return nil, imaging.Cursor{}, fmt.Errorf("capture returned no frame")
	}
	f := r.Frame
	img, err := imaging.FromBGRA(f.Width, f.Height, f.Stride, f.Pix)
	if err != nil {
		return nil, imaging.Cursor{}, err
	}
	o.s.mu.Lock()
	o.s.width, o.s.height = f.Width, f.Height
	o.s.mu.Unlock()
	cur := o.s.cursorSnapshot(o.ctx, o.w)
	if drawCursor {
		imaging.DrawCursor(img, cur)
	}
	return img, cur, nil
}

// Screenshot captures, scales and encodes the screen.
func (o *Ops) Screenshot(opt ShotOptions) (*Shot, error) {
	srv := o.s.m.Config().Server
	draw := srv.EffectiveDrawCursor()
	if opt.DrawCursor != nil {
		draw = *opt.DrawCursor
	}
	img, cur, err := o.CaptureRGBA(draw)
	if err != nil {
		return nil, err
	}
	maxW := opt.MaxWidth
	if maxW <= 0 {
		maxW = srv.EffectiveMaxWidth()
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()

	if opt.Region != nil {
		// Zoom: region is in the agent's current coordinate system; the coordinate system is not changed.
		sc := o.scale()
		rr := *opt.Region
		real := image.Rect(
			int(math.Floor(float64(rr.Min.X)/sc)), int(math.Floor(float64(rr.Min.Y)/sc)),
			int(math.Ceil(float64(rr.Max.X)/sc)), int(math.Ceil(float64(rr.Max.Y)/sc)))
		real = real.Intersect(img.Bounds())
		if real.Empty() {
			return nil, fmt.Errorf("region is outside the screen (screenshot coordinates are %dx%d)",
				int(math.Round(float64(w)*sc)), int(math.Round(float64(h)*sc)))
		}
		crop := imaging.Crop(img, real)
		zoom := imaging.Scale(real.Dx(), real.Dy(), maxW) // never upscale beyond native pixels
		out := imaging.Resize(crop, zoom)
		data, mime, err := imaging.Encode(out, opt.Format, opt.Quality, 3<<20)
		if err != nil {
			return nil, err
		}
		x0, y0 := o.ToShot(real.Min.X, real.Min.Y)
		return &Shot{
			Data: data, MIME: mime, Image: out,
			Width: out.Bounds().Dx(), Height: out.Bounds().Dy(), ScreenWidth: w, ScreenHeight: h,
			Scale: sc, Region: []int{x0, y0, rr.Dx(), rr.Dy()}, Zoom: zoom / sc,
			CursorX: int(math.Round(float64(cur.X) * sc)), CursorY: int(math.Round(float64(cur.Y) * sc)),
		}, nil
	}

	sc := imaging.Scale(w, h, maxW)
	if !opt.KeepScale {
		o.s.mu.Lock()
		o.s.lastScale = sc
		o.s.mu.Unlock()
	}
	out := imaging.Resize(img, sc)
	data, mime, err := imaging.Encode(out, opt.Format, opt.Quality, 3<<20)
	if err != nil {
		return nil, err
	}
	return &Shot{
		Data: data, MIME: mime, Image: out,
		Width: out.Bounds().Dx(), Height: out.Bounds().Dy(), ScreenWidth: w, ScreenHeight: h, Scale: sc,
		CursorX: int(math.Round(float64(cur.X) * sc)), CursorY: int(math.Round(float64(cur.Y) * sc)),
	}, nil
}

// ---- status / waiting ----

type Status struct {
	Connected bool `json:"connected"`
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	DirtySeq  int  `json:"dirty_seq"`
	IdleMs    int  `json:"idle_ms"`
}

func (o *Ops) Status() (Status, error) {
	r, err := o.call("status", nil)
	if err != nil {
		return Status{}, err
	}
	var st Status
	err = r.Decode(&st)
	return st, err
}

// WaitStable waits until the screen has not changed for stable, up to maxWait.
func (o *Ops) WaitStable(stable, maxWait time.Duration) (bool, error) {
	deadline := time.Now().Add(maxWait)
	for {
		st, err := o.Status()
		if err != nil {
			return false, err
		}
		if st.IdleMs < 0 || time.Duration(st.IdleMs)*time.Millisecond >= stable {
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		wait := stable - time.Duration(st.IdleMs)*time.Millisecond
		wait = max(30*time.Millisecond, min(wait, 100*time.Millisecond))
		if err := o.Sleep(wait); err != nil {
			return false, err
		}
	}
}

// ---- mouse ----

func (o *Ops) mouse(flags uint16, x, y int) error {
	_, err := o.call("mouse", map[string]any{"flags": flags, "x": x, "y": y})
	return err
}

func (o *Ops) mouseEx(flags uint16, x, y int) error {
	_, err := o.call("mouse_ex", map[string]any{"flags": flags, "x": x, "y": y})
	return err
}

// Move moves the pointer to desktop coordinates.
func (o *Ops) Move(x, y int) error { return o.mouse(keymap.PtrMove, x, y) }

// Button presses or releases a mouse button at desktop coordinates.
func (o *Ops) Button(button string, down bool, x, y int) error {
	f, ext, err := keymap.ButtonFlags(button)
	if err != nil {
		return err
	}
	if ext {
		if down {
			f |= keymap.PtrXDown
		}
		return o.mouseEx(f, x, y)
	}
	if down {
		f |= keymap.PtrDown
	}
	return o.mouse(f, x, y)
}

// Click clicks count times at desktop coordinates, holding modifiers.
func (o *Ops) Click(x, y int, button string, count int, modifiers []string) error {
	if count < 1 {
		count = 1
	}
	if err := o.KeysDown(modifiers); err != nil {
		return err
	}
	defer func() { _ = o.KeysUp(modifiers) }()
	if err := o.Move(x, y); err != nil {
		return err
	}
	if err := o.Sleep(30 * time.Millisecond); err != nil {
		return err
	}
	for i := 0; i < count; i++ {
		if err := o.Button(button, true, x, y); err != nil {
			return err
		}
		if err := o.Sleep(20 * time.Millisecond); err != nil {
			return err
		}
		if err := o.Button(button, false, x, y); err != nil {
			return err
		}
		if i < count-1 {
			if err := o.Sleep(60 * time.Millisecond); err != nil {
				return err
			}
		}
	}
	return nil
}

// Drag presses at (x1,y1), moves smoothly to (x2,y2) and releases.
func (o *Ops) Drag(x1, y1, x2, y2 int, button string, modifiers []string) error {
	if err := o.KeysDown(modifiers); err != nil {
		return err
	}
	defer func() { _ = o.KeysUp(modifiers) }()
	if err := o.Move(x1, y1); err != nil {
		return err
	}
	_ = o.Sleep(30 * time.Millisecond)
	if err := o.Button(button, true, x1, y1); err != nil {
		return err
	}
	dist := math.Hypot(float64(x2-x1), float64(y2-y1))
	steps := clamp(int(dist/20), 5, 40)
	for i := 1; i <= steps; i++ {
		x := x1 + (x2-x1)*i/steps
		y := y1 + (y2-y1)*i/steps
		if err := o.Move(x, y); err != nil {
			return err
		}
		_ = o.Sleep(15 * time.Millisecond)
	}
	_ = o.Sleep(50 * time.Millisecond)
	return o.Button(button, false, x2, y2)
}

// Scroll scrolls amount notches in direction at desktop coordinates.
func (o *Ops) Scroll(x, y int, direction string, amount int) error {
	if amount <= 0 {
		amount = 3
	}
	var horizontal, negative bool
	switch strings.ToLower(direction) {
	case "", "down":
		negative = true
	case "up":
	case "left":
		horizontal, negative = true, true
	case "right":
		horizontal = true
	default:
		return fmt.Errorf("direction must be up|down|left|right")
	}
	if err := o.Move(x, y); err != nil {
		return err
	}
	for i := 0; i < amount; i++ {
		if err := o.mouse(keymap.WheelFlags(horizontal, negative), x, y); err != nil {
			return err
		}
		_ = o.Sleep(20 * time.Millisecond)
	}
	return nil
}

// ---- keyboard ----

func (o *Ops) key(k keymap.Key, down bool) error {
	_, err := o.call("key", map[string]any{"scancode": k.Code, "extended": k.Extended, "down": down})
	return err
}

var shiftKey, _, _ = keymap.Lookup("shift")

// KeysDown presses keys in order.
func (o *Ops) KeysDown(names []string) error {
	for _, n := range names {
		k, sh, err := keymap.Lookup(n)
		if err != nil {
			return err
		}
		if sh {
			if err := o.key(shiftKey, true); err != nil {
				return err
			}
		}
		if err := o.key(k, true); err != nil {
			return err
		}
	}
	return nil
}

// KeysUp releases keys in reverse order.
func (o *Ops) KeysUp(names []string) error {
	var firstErr error
	for i := len(names) - 1; i >= 0; i-- {
		k, sh, err := keymap.Lookup(names[i])
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := o.key(k, false); err != nil && firstErr == nil {
			firstErr = err
		}
		if sh {
			_ = o.key(shiftKey, false)
		}
	}
	return firstErr
}

// Hotkey presses keys together and releases them in reverse order.
func (o *Ops) Hotkey(names []string) error {
	for _, n := range names {
		if _, _, err := keymap.Lookup(n); err != nil {
			return err
		}
	}
	if err := o.KeysDown(names); err != nil {
		_ = o.KeysUp(names)
		return err
	}
	_ = o.Sleep(30 * time.Millisecond)
	return o.KeysUp(names)
}

// Press taps each key in sequence, repeat times.
func (o *Ops) Press(names []string, repeat int) error {
	if repeat < 1 {
		repeat = 1
	}
	for r := 0; r < repeat; r++ {
		for _, n := range names {
			if err := o.Hotkey([]string{n}); err != nil {
				return err
			}
			_ = o.Sleep(20 * time.Millisecond)
		}
	}
	return nil
}

// Type types arbitrary Unicode text (newline = Enter, tab = Tab).
func (o *Ops) Type(text string) error {
	const chunk = 24
	for len(text) > 0 {
		n, i := 0, 0
		for i < len(text) && n < chunk {
			_, sz := utf8.DecodeRuneInString(text[i:])
			i += sz
			n++
		}
		if _, err := o.call("unicode", map[string]any{"text": text[:i]}); err != nil {
			return err
		}
		text = text[i:]
		if len(text) > 0 {
			if err := o.Sleep(15 * time.Millisecond); err != nil {
				return err
			}
		}
	}
	return nil
}

// ---- clipboard / display ----

func (o *Ops) ClipboardGet() (string, error) {
	ctx, cancel := context.WithTimeout(o.ctx, 10*time.Second)
	defer cancel()
	r, err := o.w.Call(ctx, "clipboard_get", map[string]any{"timeout_ms": 5000})
	if err != nil {
		return "", err
	}
	var d struct{ Text string }
	err = r.Decode(&d)
	return d.Text, err
}

func (o *Ops) ClipboardSet(text string) error {
	_, err := o.call("clipboard_set", map[string]any{"text": text})
	return err
}

// Resize asks the server to change the desktop size and waits for it.
func (o *Ops) Resize(w, h int) (int, int, error) {
	if _, err := o.call("resize", map[string]any{"width": w, "height": h}); err != nil {
		return 0, 0, err
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		_ = o.Sleep(200 * time.Millisecond)
		st, err := o.Status()
		if err != nil {
			return 0, 0, err
		}
		if st.Width == w&^1 && st.Height == h {
			o.s.mu.Lock()
			o.s.width, o.s.height, o.s.lastScale = st.Width, st.Height, 0
			o.s.mu.Unlock()
			return st.Width, st.Height, nil
		}
	}
	cw, ch := o.ScreenSize()
	return cw, ch, fmt.Errorf("server did not apply the new resolution (still %dx%d)", cw, ch)
}

// CtrlAltDel sends the secure attention sequence.
func (o *Ops) CtrlAltDel() error { return o.Hotkey([]string{"ctrl", "alt", "delete"}) }

// ---- live view (UI takeover) ----

// SendDOMKey presses or releases a key given as a browser KeyboardEvent.code
// (e.g. "KeyA", "ShiftLeft"). Used by the live view WebSocket.
func (o *Ops) SendDOMKey(code string, down bool) error {
	k, ok := keymap.FromDOMCode(code)
	if !ok {
		return fmt.Errorf("unsupported key %q", code)
	}
	return o.key(k, down)
}

// Wheel scrolls vertically by notches at desktop coordinates; negative notches scroll down.
func (o *Ops) Wheel(x, y, notches int) error {
	return o.wheel(x, y, notches, false)
}

// WheelH scrolls horizontally; negative notches scroll left.
func (o *Ops) WheelH(x, y, notches int) error {
	return o.wheel(x, y, notches, true)
}

func (o *Ops) wheel(x, y, notches int, horizontal bool) error {
	if notches == 0 {
		return nil
	}
	negative := notches < 0
	if negative {
		notches = -notches
	}
	if err := o.Move(x, y); err != nil {
		return err
	}
	for i := 0; i < notches; i++ {
		if err := o.mouse(keymap.WheelFlags(horizontal, negative), x, y); err != nil {
			return err
		}
		_ = o.Sleep(15 * time.Millisecond)
	}
	return nil
}

// CursorOverlay returns the pointer state for the live view (refreshing a stale shape).
func (o *Ops) CursorOverlay() imaging.Cursor {
	return o.s.cursorSnapshot(o.ctx, o.w)
}

// CursorShapeSeq identifies the current pointer shape (changes when the shape changes).
func (o *Ops) CursorShapeSeq() int {
	o.s.mu.Lock()
	defer o.s.mu.Unlock()
	return o.s.cursor.shapeSeq
}
