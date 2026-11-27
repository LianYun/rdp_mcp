package session

import (
	"context"
	"encoding/base64"
	"strconv"
	"time"

	"github.com/LianYun/rdp_mcp/internal/config"
)

// TestResult is the outcome of a trial connection (POST /machines/{name}/test).
type TestResult struct {
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
	Hint       string `json:"hint,omitempty"` // probable cause of error, e.g. "local_network"
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	FreeRDP    string `json:"freerdp,omitempty"`
	SSH        string `json:"ssh,omitempty"`        // macOS: "ok" or the SSH error
	Screenshot string `json:"screenshot,omitempty"` // base64 JPEG
}

// TestConnect connects to a machine with the given (possibly overridden) definition
// using a temporary worker, captures one screenshot and disconnects. The machine's
// regular session, if any, is not touched.
func (m *Manager) TestConnect(ctx context.Context, mc config.Machine) *TestResult {
	r := m.Config().Resolve(mc)
	r.EnableDrive = false // no share side effects
	r.ReconnectAttempts = 0
	s := &Session{m: m, name: mc.Name + "-test", cfg: r, state: StateIdle, mode: ModeAgent, quiet: true}
	res := &TestResult{}
	log := m.opts.Logger.With("machine", mc.Name, "test", true)

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	s.op.Lock()
	err := s.connect(ctx)
	s.op.Unlock()
	if err != nil {
		res.Error = err.Error()
		s.mu.Lock()
		res.Hint = s.lastHint
		s.mu.Unlock()
		return res
	}
	defer s.Disconnect()

	var shot *Shot
	if err := s.Do(ctx, SourceAdmin, func(o *Ops) error {
		var e error
		shot, e = o.Screenshot(ShotOptions{MaxWidth: 1024, Format: "jpeg"})
		return e
	}); err != nil {
		res.Error = "connected, but the screenshot failed: " + err.Error()
		return res
	}
	res.OK = true
	if r.IsMac() && r.SSHEnabled {
		res.SSH = "ok"
		if out, err := s.SSHRun(ctx, "echo ok", nil); err != nil {
			res.SSH, res.OK = err.Error(), false
			res.Error = "screen sharing works, but SSH (Remote Login) failed: " + err.Error()
		} else if out.ExitCode != 0 {
			res.SSH, res.OK = "exit "+strconv.Itoa(out.ExitCode), false
		}
	}
	res.Width, res.Height = shot.ScreenWidth, shot.ScreenHeight
	res.FreeRDP = s.workerFreeRDP()
	res.Screenshot = base64.StdEncoding.EncodeToString(shot.Data)
	log.Info("test connection ok", "width", res.Width, "height", res.Height)
	return res
}

func (s *Session) workerFreeRDP() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.w == nil {
		return ""
	}
	return s.w.Hello.FreeRDP
}
