package session

import (
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/LianYun/rdp_mcp/internal/config"
	"github.com/LianYun/rdp_mcp/internal/imaging"
	"github.com/LianYun/rdp_mcp/internal/worker"
)

type Session struct {
	m    *Manager
	name string

	op sync.Mutex // serializes operations on the remote desktop

	mu           sync.Mutex
	cfg          config.Resolved
	state        State
	mode         ControlMode
	lastErr      string
	w            *worker.Client
	gen          int
	width        int
	height       int
	lastActivity time.Time
	connectedAt  time.Time
	lastScale    float64
	manual       bool // disconnect requested
	cursor       cursorState
	viewers      map[chan struct{}]struct{}
}

type cursorState struct {
	x, y       int
	visible    bool
	shapeSeq   int
	cachedSeq  int
	shape      image.Image
	hotX, hotY int
}

// Info is a snapshot for listings.
type Info struct {
	Name         string      `json:"name"`
	Description  string      `json:"description,omitempty"`
	Tags         []string    `json:"tags,omitempty"`
	Host         string      `json:"host"`
	Port         int         `json:"port"`
	Username     string      `json:"username"`
	Enabled      bool        `json:"enabled"`
	State        State       `json:"state"`
	ControlMode  ControlMode `json:"control_mode"`
	Width        int         `json:"width,omitempty"`
	Height       int         `json:"height,omitempty"`
	LastError    string      `json:"last_error,omitempty"`
	LastActivity *time.Time  `json:"last_activity,omitempty"`
	ConnectedAt  *time.Time  `json:"connected_at,omitempty"`
	Viewers      int         `json:"viewers"`
}

func (s *Session) Name() string { return s.name }

func (s *Session) Resolved() config.Resolved {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

func (s *Session) Info() Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	in := Info{
		Name: s.name, Description: s.cfg.Description, Tags: s.cfg.Tags,
		Host: s.cfg.Host, Port: s.cfg.Port, Username: s.cfg.Username, Enabled: s.cfg.IsEnabled,
		State: s.state, ControlMode: s.mode, LastError: s.lastErr, Viewers: len(s.viewers),
	}
	if s.state == StateConnected {
		in.Width, in.Height = s.width, s.height
		t := s.connectedAt
		in.ConnectedAt = &t
	}
	if !s.lastActivity.IsZero() {
		t := s.lastActivity
		in.LastActivity = &t
	}
	return in
}

func (s *Session) Mode() ControlMode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode
}

func (s *Session) SetMode(mode ControlMode) {
	s.mu.Lock()
	changed := s.mode != mode
	s.mode = mode
	s.mu.Unlock()
	if changed {
		s.m.opts.Logger.Info("control mode changed", "machine", s.name, "mode", mode)
		s.m.opts.Bus.Publish("control_mode", map[string]any{"machine": s.name, "mode": mode})
	}
}

func (s *Session) setState(st State, errMsg string) {
	s.mu.Lock()
	s.state = st
	if errMsg != "" || st == StateConnected {
		s.lastErr = errMsg
	}
	info := map[string]any{"machine": s.name, "state": st, "error": errMsg}
	if st == StateConnected {
		info["resolution"] = []int{s.width, s.height}
	}
	s.mu.Unlock()
	s.m.opts.Bus.Publish("machine_state", info)
}

func (s *Session) checkSource(src Source) error {
	if src != SourceAgent && src != SourceHuman {
		return nil
	}
	s.mu.Lock()
	mode := s.mode
	s.mu.Unlock()
	switch {
	case src == SourceAgent && mode != ModeAgent:
		return &ControlError{Machine: s.name, Mode: mode}
	case src == SourceHuman && mode != ModeHuman:
		return errors.New("take over the machine before sending input")
	}
	return nil
}

func (s *Session) shareDir() string { return filepath.Join(s.m.opts.DataDir, "shares", s.name) }

// ShareDir is the local directory mapped to \\tsclient\mcp.
func (s *Session) ShareDir() string { return s.shareDir() }

// connect starts a worker and connects. Caller holds s.op.
func (s *Session) connect(ctx context.Context) error {
	s.mu.Lock()
	r := s.cfg
	s.manual = false
	s.lastErr = ""
	s.mu.Unlock()
	s.setState(StateConnecting, "")
	log := s.m.opts.Logger.With("machine", s.name)

	pass, err := config.ResolvePassword(ctx, r.Machine, s.m.opts.KeychainGet)
	if err != nil {
		s.setState(StateFailed, err.Error())
		return err
	}
	dataDir := s.m.opts.DataDir
	logDir := filepath.Join(dataDir, "logs")
	_ = os.MkdirAll(logDir, 0o700)
	// FreeRDP's own certificate store is never used (the service pins certificates in known_hosts),
	// so its "host key changed" warnings are noise outside debug mode.
	env := []string{"WLOG_LEVEL=WARN", "WLOG_FILTER=com.freerdp.crypto:FATAL"}
	if s.m.opts.WorkerDebug {
		env = []string{"WLOG_LEVEL=DEBUG"}
	}
	w, err := worker.Start(ctx, worker.StartOptions{
		WorkerPath: s.m.opts.WorkerPath,
		SocketDir:  filepath.Join(dataDir, "run", "ipc"),
		Name:       s.name,
		LogFile:    filepath.Join(logDir, "worker-"+s.name+".log"),
		Env:        env,
		Logger:     log,
	})
	if err != nil {
		s.setState(StateFailed, err.Error())
		return err
	}

	s.mu.Lock()
	s.gen++
	gen := s.gen
	s.w = w
	s.mu.Unlock()
	go s.eventLoop(w, gen)

	params := map[string]any{
		"host":              r.Host,
		"port":              r.Port,
		"username":          r.Username,
		"password":          pass,
		"domain":            r.Domain,
		"width":             r.Width,
		"height":            r.Height,
		"security":          r.Security,
		"cert":              r.Cert,
		"clipboard":         r.EnableClipboard,
		"disable_wallpaper": r.DisableWallpaper,
		"config_dir":        filepath.Join(dataDir, "freerdp"),
	}
	if r.EnableDrive {
		dir := s.shareDir()
		if err := os.MkdirAll(filepath.Join(dir, "jobs"), 0o700); err == nil {
			params["drive_path"] = dir
			params["drive_name"] = "mcp"
		}
	}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	reply, err := w.Call(cctx, "connect", params)
	if err != nil {
		w.Kill()
		msg := err.Error()
		s.mu.Lock()
		if s.lastErr != "" && s.state == StateConnecting && isCertErr(s.lastErr) {
			msg = s.lastErr + " (" + msg + ")"
		}
		s.mu.Unlock()
		s.setState(StateFailed, msg)
		log.Warn("connect failed", "err", msg)
		return errors.New(msg)
	}
	var size struct{ Width, Height int }
	_ = reply.Decode(&size)
	s.mu.Lock()
	s.width, s.height = size.Width, size.Height
	s.connectedAt = time.Now()
	s.lastActivity = time.Now()
	s.lastScale = 0
	s.cursor = cursorState{visible: true, cachedSeq: -1}
	viewers := len(s.viewers)
	s.mu.Unlock()
	if viewers > 0 {
		_, _ = w.Call(ctx, "subscribe", map[string]any{"on": true})
	}
	waited, logon := waitFirstFrame(ctx, w)
	log.Debug("desktop ready", "waited", waited, "logon_notified", logon)
	s.setState(StateConnected, "")
	log.Info("connected", "width", size.Width, "height", size.Height, "freerdp", w.Hello.FreeRDP)
	return nil
}

func isCertErr(s string) bool { return len(s) >= 11 && s[:11] == "certificate" }

func (s *Session) eventLoop(w *worker.Client, gen int) {
	log := s.m.opts.Logger.With("machine", s.name)
	for {
		select {
		case ev := <-w.Events():
			s.handleEvent(w, ev)
		case <-w.Done():
			// drain remaining events
			for {
				select {
				case ev := <-w.Events():
					s.handleEvent(w, ev)
					continue
				default:
				}
				break
			}
			s.mu.Lock()
			current := s.gen == gen
			wasConnected := s.state == StateConnected
			manual := s.manual
			attempts := s.cfg.ReconnectAttempts
			if current {
				s.w = nil
			}
			s.mu.Unlock()
			if !current {
				return
			}
			if wasConnected && !manual {
				log.Warn("session lost", "err", w.Err())
				if attempts > 0 {
					s.setState(StateReconnecting, "connection lost")
					go s.reconnectLoop(gen, attempts)
				} else {
					s.setState(StateFailed, "connection lost")
				}
			} else if manual {
				s.setState(StateIdle, "")
			}
			return
		}
	}
}

func (s *Session) handleEvent(w *worker.Client, ev worker.Event) {
	switch ev.Name {
	case "cert_verify":
		var c struct {
			Host        string `json:"host"`
			Port        int    `json:"port"`
			Fingerprint string `json:"fingerprint"`
			Subject     string `json:"subject"`
			Issuer      string `json:"issuer"`
		}
		_ = ev.Decode(&c)
		accept, reason := s.decideCert(c.Host, c.Port, c.Fingerprint)
		if !accept {
			s.mu.Lock()
			s.lastErr = reason
			s.mu.Unlock()
			s.m.opts.Bus.Publish("cert_verify", map[string]any{
				"machine": s.name, "host": c.Host, "port": c.Port,
				"fingerprint": c.Fingerprint, "subject": c.Subject, "issuer": c.Issuer, "reason": reason,
			})
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			a := 0
			if accept {
				a = 2
			}
			_, _ = w.Call(ctx, "cert_reply", map[string]any{"accept": a})
		}()
	case "disconnected":
		var d struct{ Reason string }
		_ = ev.Decode(&d)
		s.m.opts.Logger.Info("worker reported disconnect", "machine", s.name, "reason", d.Reason)
		s.mu.Lock()
		if d.Reason != "" && !s.manual {
			s.lastErr = d.Reason
		}
		s.mu.Unlock()
	case "resized":
		var d struct{ Width, Height int }
		_ = ev.Decode(&d)
		s.mu.Lock()
		s.width, s.height = d.Width, d.Height
		s.lastScale = 0
		s.mu.Unlock()
		s.m.opts.Bus.Publish("machine_state", map[string]any{"machine": s.name, "state": StateConnected, "resolution": []int{d.Width, d.Height}})
	case "cursor":
		var d struct {
			X, Y     int
			Visible  bool
			ShapeSeq int `json:"shape_seq"`
		}
		_ = ev.Decode(&d)
		s.mu.Lock()
		s.cursor.x, s.cursor.y, s.cursor.visible, s.cursor.shapeSeq = d.X, d.Y, d.Visible, d.ShapeSeq
		s.mu.Unlock()
	case "frame_dirty":
		s.mu.Lock()
		for ch := range s.viewers {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
		s.mu.Unlock()
	}
}

func (s *Session) decideCert(host string, port int, fp string) (bool, string) {
	r := s.Resolved()
	key := host + ":" + strconv.Itoa(port)
	got := NormalizeFingerprint(fp)
	switch r.Cert {
	case "ignore":
		return true, ""
	case "fingerprint":
		if got == NormalizeFingerprint(r.CertFingerprint) {
			return true, ""
		}
		return false, fmt.Sprintf("certificate fingerprint mismatch for %s: got %s", key, fp)
	default: // tofu
		known, ok := s.m.known.Get(key)
		if !ok {
			if err := s.m.known.Set(key, fp); err != nil {
				s.m.opts.Logger.Warn("save known_hosts", "err", err)
			}
			s.m.opts.Logger.Info("trusted new certificate (TOFU)", "machine", s.name, "host", key, "fingerprint", fp)
			return true, ""
		}
		if known == got {
			return true, ""
		}
		return false, fmt.Sprintf("certificate for %s changed (now %s); if expected, run `rdp-mcp trust %s` or remove the entry from %s",
			key, fp, s.name, filepath.Join(s.m.opts.DataDir, "known_hosts"))
	}
}

func (s *Session) reconnectLoop(gen, attempts int) {
	for i := 1; i <= attempts; i++ {
		delay := time.Duration(1<<min(i, 5)) * time.Second
		select {
		case <-time.After(delay):
		case <-s.m.ctx.Done():
			return
		}
		s.op.Lock()
		s.mu.Lock()
		still := s.state == StateReconnecting && s.gen == gen
		s.mu.Unlock()
		if !still {
			s.op.Unlock()
			return
		}
		ctx, cancel := context.WithTimeout(s.m.ctx, 90*time.Second)
		err := s.connect(ctx)
		cancel()
		if err == nil {
			s.op.Unlock()
			return
		}
		s.mu.Lock()
		gen = s.gen
		s.mu.Unlock()
		if i < attempts {
			s.setState(StateReconnecting, fmt.Sprintf("reconnect attempt %d/%d failed: %v", i, attempts, err))
		}
		s.op.Unlock()
	}
}

// ensure makes sure a connected worker exists. Caller holds s.op.
func (s *Session) ensure(ctx context.Context) (*worker.Client, error) {
	s.mu.Lock()
	w, st := s.w, s.state
	s.mu.Unlock()
	if w != nil && st == StateConnected {
		select {
		case <-w.Done():
		default:
			return w, nil
		}
	}
	if w != nil {
		w.Kill()
	}
	if err := s.connect(ctx); err != nil {
		return nil, fmt.Errorf("connect %s: %w", s.name, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.w == nil {
		return nil, errors.New("connection lost")
	}
	return s.w, nil
}

// Connect connects (or reconnects if already connected and force is set).
func (s *Session) Connect(ctx context.Context) error {
	s.op.Lock()
	defer s.op.Unlock()
	_, err := s.ensure(ctx)
	return err
}

// Disconnect closes the session.
func (s *Session) Disconnect() {
	s.op.Lock()
	defer s.op.Unlock()
	s.disconnectLocked()
}

func (s *Session) disconnectLocked() {
	s.mu.Lock()
	w := s.w
	s.manual = true
	s.w = nil
	s.mu.Unlock()
	if w != nil {
		w.Close()
	}
	s.setState(StateIdle, "")
}

// Reconnect drops and re-establishes the session.
func (s *Session) Reconnect(ctx context.Context) error {
	s.op.Lock()
	defer s.op.Unlock()
	s.disconnectLocked()
	_, err := s.ensure(ctx)
	return err
}

func (s *Session) reapIfIdle() {
	s.mu.Lock()
	r := s.cfg
	idle := s.state == StateConnected && r.IdleDisconnect > 0 && len(s.viewers) == 0 &&
		time.Since(s.lastActivity) > r.IdleDisconnect
	s.mu.Unlock()
	if idle && s.op.TryLock() {
		s.m.opts.Logger.Info("disconnecting idle session", "machine", s.name)
		s.disconnectLocked()
		s.op.Unlock()
	}
}

// Do runs fn with exclusive access to a connected session.
func (s *Session) Do(ctx context.Context, src Source, fn func(o *Ops) error) error {
	if err := s.checkSource(src); err != nil {
		return err
	}
	s.op.Lock()
	defer s.op.Unlock()
	w, err := s.ensure(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.lastActivity = time.Now()
	s.mu.Unlock()
	err = fn(&Ops{s: s, w: w, ctx: ctx})
	s.mu.Lock()
	s.lastActivity = time.Now()
	s.mu.Unlock()
	return err
}

// SubscribeFrames registers a live viewer; the channel is signaled when the screen changes.
func (s *Session) SubscribeFrames() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	if s.viewers == nil {
		s.viewers = map[chan struct{}]struct{}{}
	}
	s.viewers[ch] = struct{}{}
	first := len(s.viewers) == 1
	w := s.w
	s.mu.Unlock()
	if first && w != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, _ = w.Call(ctx, "subscribe", map[string]any{"on": true})
		cancel()
	}
	return ch, func() {
		s.mu.Lock()
		delete(s.viewers, ch)
		last := len(s.viewers) == 0
		w := s.w
		s.mu.Unlock()
		if last && w != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, _ = w.Call(ctx, "subscribe", map[string]any{"on": false})
			cancel()
		}
	}
}

// cursorSnapshot returns the cursor for overlay, refreshing the shape if needed. Caller holds s.op.
func (s *Session) cursorSnapshot(ctx context.Context, w *worker.Client) imaging.Cursor {
	s.mu.Lock()
	c := s.cursor
	s.mu.Unlock()
	if c.shapeSeq != c.cachedSeq {
		reply, err := w.Call(ctx, "cursor_get", map[string]any{"shape": true})
		if err == nil {
			var d struct {
				X, Y     int
				Visible  bool
				HotX     int  `json:"hot_x"`
				HotY     int  `json:"hot_y"`
				ShapeSeq int  `json:"shape_seq"`
				HasShape bool `json:"has_shape"`
			}
			_ = reply.Decode(&d)
			c.x, c.y, c.visible, c.hotX, c.hotY = d.X, d.Y, d.Visible, d.HotX, d.HotY
			c.shape = nil
			if d.HasShape && reply.Frame != nil {
				f := reply.Frame
				c.shape = imaging.BGRAToNRGBA(f.Width, f.Height, f.Stride, f.Pix)
			}
			c.shapeSeq, c.cachedSeq = d.ShapeSeq, d.ShapeSeq
			s.mu.Lock()
			s.cursor = c
			s.mu.Unlock()
		}
	}
	return imaging.Cursor{X: c.x, Y: c.y, HotX: c.hotX, HotY: c.hotY, Shape: c.shape, Visible: c.visible}
}

// CursorPos returns the last known pointer position in desktop coordinates.
func (s *Session) CursorPos() [2]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return [2]int{s.cursor.x, s.cursor.y}
}

// waitFirstFrame waits until the server finished logon and the desktop settled, so the first
// screenshot after connecting is neither blank nor the transient lock screen shown while Windows
// re-attaches an existing session. Returns how long it waited and whether logon was seen.
func waitFirstFrame(ctx context.Context, w *worker.Client) (time.Duration, bool) {
	start := time.Now()
	deadline := start.Add(15 * time.Second)
	for time.Now().Before(deadline) {
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		r, err := w.Call(cctx, "status", nil)
		cancel()
		if err != nil {
			return time.Since(start), false
		}
		var st struct {
			DirtySeq   int `json:"dirty_seq"`
			IdleMs     int `json:"idle_ms"`
			LogonCount int `json:"logon_count"`
			LogonAgo   int `json:"logon_ms_ago"`
		}
		_ = r.Decode(&st)
		logon := st.LogonCount > 0
		// After logon, require the screen to be quiet for a moment; without a logon
		// notification (some servers never send one) fall back to a longer quiet period.
		if logon && st.LogonAgo >= 500 && st.DirtySeq > 0 && st.IdleMs >= 500 {
			return time.Since(start), true
		}
		if !logon && time.Since(start) > 5*time.Second && st.DirtySeq > 0 && st.IdleMs >= 1000 {
			return time.Since(start), false
		}
		select {
		case <-ctx.Done():
			return time.Since(start), false
		case <-time.After(100 * time.Millisecond):
		}
	}
	return time.Since(start), false
}
