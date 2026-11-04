// Package mcpserver exposes the MCP tools described in docs/mcp-tools.md.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LianYun/rdp_mcp/internal/audit"
	"github.com/LianYun/rdp_mcp/internal/remote"
	"github.com/LianYun/rdp_mcp/internal/session"
)

type Server struct {
	mgr     *session.Manager
	audit   *audit.Logger
	version string
}

func New(mgr *session.Manager, al *audit.Logger, version string) *Server {
	return &Server{mgr: mgr, audit: al, version: version}
}

const instructions = `Control remote Windows desktops over RDP.
Workflow: call list_machines, then screenshot a machine to see it. All x/y coordinates are in the
coordinate space of the most recent screenshot of that machine (the server rescales them).
mouse and keyboard actions return a fresh screenshot by default, so you rarely need a separate screenshot call.
Use keyboard action "type" for text (any language), "hotkey" for combinations like ["ctrl","c"] or ["win","r"].
run_command executes PowerShell/cmd on the remote desktop and returns stdout/stderr; it needs an unlocked desktop.
If a tool says the machine is under human control or paused, stop and wait for the operator.`

// MCP returns a configured MCP server.
func (s *Server) MCP() *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "rdp-mcp", Title: "RDP MCP", Version: s.version},
		&mcp.ServerOptions{Instructions: instructions})

	mcp.AddTool(srv, &mcp.Tool{Name: "list_machines",
		Description: "List configured Windows machines with connection state, control mode and resolution."},
		wrap(s, "list_machines", s.listMachines))
	mcp.AddTool(srv, &mcp.Tool{Name: "screenshot",
		Description: "Capture the screen of a machine. Connects automatically if needed. Coordinates used by other tools refer to this image."},
		wrap(s, "screenshot", s.screenshot))
	mcp.AddTool(srv, &mcp.Tool{Name: "mouse",
		Description: "Mouse action at screenshot coordinates: move, click, double_click, right_click, middle_click, down, up, drag (x,y -> to_x,to_y), scroll (direction + amount)."},
		wrap(s, "mouse", s.mouse))
	mcp.AddTool(srv, &mcp.Tool{Name: "keyboard",
		Description: "Keyboard action: type (any Unicode text; \\n = Enter), press (tap keys in sequence, e.g. [\"enter\"]), hotkey (combination, e.g. [\"ctrl\",\"shift\",\"esc\"]), down/up (hold or release keys). Key names: ctrl shift alt win enter esc tab backspace delete home end pageup pagedown up down left right space f1-f24 a-z 0-9 and symbols."},
		wrap(s, "keyboard", s.keyboard))
	mcp.AddTool(srv, &mcp.Tool{Name: "clipboard",
		Description: "Read (get) or write (set) the remote clipboard text."},
		wrap(s, "clipboard", s.clipboard))
	mcp.AddTool(srv, &mcp.Tool{Name: "run_command",
		Description: "Run a PowerShell (default) or cmd command on the remote machine and return exit_code, stdout and stderr. Works by typing into the Run dialog, so the desktop must be unlocked and not blocked by a modal dialog."},
		wrap(s, "run_command", s.runCommand))
	mcp.AddTool(srv, &mcp.Tool{Name: "file",
		Description: "Transfer files: upload (local_path or content -> remote_path), download (remote_path -> local_path, or return text content if local_path is empty), list (remote directory). local_path refers to the machine running this MCP server."},
		wrap(s, "file", s.file))
	mcp.AddTool(srv, &mcp.Tool{Name: "session",
		Description: "Manage the RDP session: connect, disconnect, reconnect, status, set_resolution (width,height), send_ctrl_alt_del."},
		wrap(s, "session", s.sessionTool))
	mcp.AddTool(srv, &mcp.Tool{Name: "wait",
		Description: "Wait a number of seconds, or until the screen stops changing (until_stable). Returns a screenshot by default."},
		wrap(s, "wait", s.wait))
	return srv
}

type handler[In any] func(ctx context.Context, in In) (*mcp.CallToolResult, error)

// wrap adds auditing and converts errors into tool errors.
func wrap[In any](s *Server, tool string, h handler[In]) mcp.ToolHandlerFor[In, any] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		machine := machineOf(in)
		id := s.audit.Begin(tool, machine, in)
		res, err := h(ctx, in)
		if err != nil {
			res = &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: errorText(err)}}}
		}
		s.audit.End(id, tool, machine, in, time.Since(start), res.IsError, summary(res))
		return res, nil, nil
	}
}

func errorText(err error) string {
	var ce *session.ControlError
	if errors.As(err, &ce) {
		return "BLOCKED: " + ce.Error()
	}
	return "Error: " + err.Error()
}

func machineOf(in any) string {
	b, _ := json.Marshal(in)
	var m struct{ Machine string }
	_ = json.Unmarshal(b, &m)
	return m.Machine
}

func summary(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			if len(t.Text) > 300 {
				return t.Text[:300] + "..."
			}
			return t.Text
		}
	}
	return ""
}

func textResult(v any) *mcp.CallToolResult {
	var text string
	switch t := v.(type) {
	case string:
		text = t
	default:
		b, _ := json.MarshalIndent(v, "", "  ")
		text = string(b)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func shotContent(shot *session.Shot, note string) []mcp.Content {
	var meta string
	if len(shot.Region) == 4 {
		meta = fmt.Sprintf("zoomed view of region x=%d y=%d w=%d h=%d (in screenshot coordinates), image %dx%d, zoom %.2fx. "+
			"To click something in this image at pixel (px,py), use x=%d+px/%.2f, y=%d+py/%.2f. The coordinate system is unchanged.",
			shot.Region[0], shot.Region[1], shot.Region[2], shot.Region[3], shot.Width, shot.Height, shot.Zoom,
			shot.Region[0], shot.Zoom, shot.Region[1], shot.Zoom)
	} else {
		meta = fmt.Sprintf("screenshot %dx%d (screen %dx%d, scale %.4f), cursor at (%d,%d)",
			shot.Width, shot.Height, shot.ScreenWidth, shot.ScreenHeight, shot.Scale, shot.CursorX, shot.CursorY)
	}
	if note != "" {
		meta = note + "\n" + meta
	}
	return []mcp.Content{
		&mcp.TextContent{Text: meta},
		&mcp.ImageContent{Data: shot.Data, MIMEType: shot.MIME},
	}
}

func (s *Server) get(name string) (*session.Session, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("machine is required (see list_machines)")
	}
	return s.mgr.Get(name)
}

// afterAction optionally waits for the screen to settle and appends a screenshot.
func afterAction(o *session.Ops, want *bool, note string) (*mcp.CallToolResult, error) {
	if want != nil && !*want {
		return textResult(note), nil
	}
	_, _ = o.WaitStable(300*time.Millisecond, 2*time.Second)
	shot, err := o.Screenshot(session.ShotOptions{})
	if err != nil {
		return textResult(note + " (screenshot failed: " + err.Error() + ")"), nil
	}
	return &mcp.CallToolResult{Content: shotContent(shot, note)}, nil
}

// ---- list_machines ----

type listIn struct{}

type machineView struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	State       string   `json:"state"`
	ControlMode string   `json:"control_mode"`
	Resolution  []int    `json:"resolution,omitempty"`
	LastError   string   `json:"last_error,omitempty"`
}

func (s *Server) listMachines(ctx context.Context, _ listIn) (*mcp.CallToolResult, error) {
	var out []machineView
	for _, sess := range s.mgr.List() {
		in := sess.Info()
		if !in.Enabled {
			continue
		}
		v := machineView{Name: in.Name, Description: in.Description, Tags: in.Tags,
			State: string(in.State), ControlMode: string(in.ControlMode), LastError: in.LastError}
		if in.Width > 0 {
			v.Resolution = []int{in.Width, in.Height}
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return textResult("No machines configured. Add machines to .rdp_mcp/configs.yaml."), nil
	}
	return textResult(out), nil
}

// ---- screenshot ----

type Region struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

type screenshotIn struct {
	Machine  string  `json:"machine" jsonschema:"machine name from list_machines"`
	Region   *Region `json:"region,omitempty" jsonschema:"zoom into this region (in coordinates of the last full screenshot) at up to native resolution; does not change the coordinate system"`
	MaxWidth int     `json:"max_width,omitempty" jsonschema:"long side of the returned image in pixels (default 1280). For full screenshots this sets the coordinate scale for following actions"`
	Format   string  `json:"format,omitempty" jsonschema:"png (default) or jpeg"`
}

func (s *Server) screenshot(ctx context.Context, in screenshotIn) (*mcp.CallToolResult, error) {
	sess, err := s.get(in.Machine)
	if err != nil {
		return nil, err
	}
	var res *mcp.CallToolResult
	err = sess.Do(ctx, session.SourceAdmin, func(o *session.Ops) error {
		opt := session.ShotOptions{MaxWidth: in.MaxWidth, Format: in.Format}
		if in.Region != nil {
			if in.Region.Width <= 0 || in.Region.Height <= 0 {
				return errors.New("region width and height must be positive")
			}
			r := image.Rect(in.Region.X, in.Region.Y, in.Region.X+in.Region.Width, in.Region.Y+in.Region.Height)
			opt.Region = &r
		}
		shot, err := o.Screenshot(opt)
		if err != nil {
			return err
		}
		note := ""
		if m := sess.Mode(); m != session.ModeAgent {
			note = fmt.Sprintf("NOTE: control mode is %q; input actions are currently blocked.", m)
		}
		res = &mcp.CallToolResult{Content: shotContent(shot, note)}
		return nil
	})
	return res, err
}

// ---- mouse ----

type mouseIn struct {
	Machine         string   `json:"machine" jsonschema:"machine name"`
	Action          string   `json:"action" jsonschema:"move | click | double_click | right_click | middle_click | down | up | drag | scroll"`
	X               *int     `json:"x,omitempty" jsonschema:"x in screenshot coordinates (default: current cursor position)"`
	Y               *int     `json:"y,omitempty" jsonschema:"y in screenshot coordinates (default: current cursor position)"`
	Button          string   `json:"button,omitempty" jsonschema:"left (default) | right | middle | x1 | x2 (for click/down/up/drag)"`
	ToX             *int     `json:"to_x,omitempty" jsonschema:"drag end x"`
	ToY             *int     `json:"to_y,omitempty" jsonschema:"drag end y"`
	Direction       string   `json:"direction,omitempty" jsonschema:"scroll direction: up | down | left | right"`
	Amount          int      `json:"amount,omitempty" jsonschema:"scroll notches (default 3)"`
	Modifiers       []string `json:"modifiers,omitempty" jsonschema:"keys held during the action, e.g. [\"ctrl\"]"`
	ScreenshotAfter *bool    `json:"screenshot_after,omitempty" jsonschema:"return a screenshot after the action (default true)"`
}

func (s *Server) mouse(ctx context.Context, in mouseIn) (*mcp.CallToolResult, error) {
	sess, err := s.get(in.Machine)
	if err != nil {
		return nil, err
	}
	var res *mcp.CallToolResult
	err = sess.Do(ctx, session.SourceAgent, func(o *session.Ops) error {
		cur := sess.CursorPos()
		pos := func(px, py *int) (int, int) {
			if px == nil || py == nil {
				return cur[0], cur[1]
			}
			return o.ToReal(*px, *py)
		}
		x, y := pos(in.X, in.Y)
		var err error
		switch strings.ToLower(in.Action) {
		case "move":
			err = o.Move(x, y)
		case "click", "left_click":
			err = o.Click(x, y, in.Button, 1, in.Modifiers)
		case "double_click":
			err = o.Click(x, y, in.Button, 2, in.Modifiers)
		case "triple_click":
			err = o.Click(x, y, in.Button, 3, in.Modifiers)
		case "right_click":
			err = o.Click(x, y, "right", 1, in.Modifiers)
		case "middle_click":
			err = o.Click(x, y, "middle", 1, in.Modifiers)
		case "down":
			if err = o.Move(x, y); err == nil {
				err = o.Button(in.Button, true, x, y)
			}
		case "up":
			err = o.Button(in.Button, false, x, y)
		case "drag":
			if in.ToX == nil || in.ToY == nil {
				return errors.New("drag requires to_x and to_y")
			}
			tx, ty := o.ToReal(*in.ToX, *in.ToY)
			err = o.Drag(x, y, tx, ty, in.Button, in.Modifiers)
		case "scroll":
			err = o.Scroll(x, y, in.Direction, in.Amount)
		default:
			return fmt.Errorf("unknown action %q", in.Action)
		}
		if err != nil {
			return err
		}
		res, err = afterAction(o, in.ScreenshotAfter, "ok: "+in.Action)
		return err
	})
	return res, err
}

// ---- keyboard ----

type keyboardIn struct {
	Machine         string   `json:"machine" jsonschema:"machine name"`
	Action          string   `json:"action" jsonschema:"type | press | hotkey | down | up"`
	Text            string   `json:"text,omitempty" jsonschema:"text for action=type"`
	Keys            []string `json:"keys,omitempty" jsonschema:"key names for press/hotkey/down/up"`
	Repeat          int      `json:"repeat,omitempty" jsonschema:"repeat count for press"`
	ScreenshotAfter *bool    `json:"screenshot_after,omitempty" jsonschema:"return a screenshot after the action (default true)"`
}

func (s *Server) keyboard(ctx context.Context, in keyboardIn) (*mcp.CallToolResult, error) {
	sess, err := s.get(in.Machine)
	if err != nil {
		return nil, err
	}
	var res *mcp.CallToolResult
	err = sess.Do(ctx, session.SourceAgent, func(o *session.Ops) error {
		var err error
		switch strings.ToLower(in.Action) {
		case "type":
			if in.Text == "" {
				return errors.New("text is required for type")
			}
			err = o.Type(in.Text)
		case "press", "key":
			if len(in.Keys) == 0 {
				return errors.New("keys is required")
			}
			err = o.Press(in.Keys, in.Repeat)
		case "hotkey":
			if len(in.Keys) == 0 {
				return errors.New("keys is required")
			}
			err = o.Hotkey(in.Keys)
		case "down":
			err = o.KeysDown(in.Keys)
		case "up":
			err = o.KeysUp(in.Keys)
		default:
			return fmt.Errorf("unknown action %q", in.Action)
		}
		if err != nil {
			return err
		}
		note := "ok: " + in.Action
		if in.Action == "type" {
			note = fmt.Sprintf("ok: typed %d characters", utf8.RuneCountInString(in.Text))
		}
		res, err = afterAction(o, in.ScreenshotAfter, note)
		return err
	})
	return res, err
}

// ---- clipboard ----

type clipboardIn struct {
	Machine string `json:"machine" jsonschema:"machine name"`
	Action  string `json:"action" jsonschema:"get | set"`
	Text    string `json:"text,omitempty" jsonschema:"text for set"`
}

func (s *Server) clipboard(ctx context.Context, in clipboardIn) (*mcp.CallToolResult, error) {
	sess, err := s.get(in.Machine)
	if err != nil {
		return nil, err
	}
	var res *mcp.CallToolResult
	err = sess.Do(ctx, session.SourceAgent, func(o *session.Ops) error {
		switch strings.ToLower(in.Action) {
		case "get":
			t, err := o.ClipboardGet()
			if err != nil {
				return err
			}
			res = textResult(t)
		case "set":
			if err := o.ClipboardSet(in.Text); err != nil {
				return err
			}
			res = textResult("ok: clipboard set; paste with keyboard hotkey [\"ctrl\",\"v\"]")
		default:
			return fmt.Errorf("action must be get or set")
		}
		return nil
	})
	return res, err
}

// ---- run_command ----

type runIn struct {
	Machine string `json:"machine" jsonschema:"machine name"`
	Command string `json:"command" jsonschema:"command or script body"`
	Shell   string `json:"shell,omitempty" jsonschema:"powershell (default) | cmd"`
	Timeout int    `json:"timeout,omitempty" jsonschema:"timeout in seconds (default 60)"`
}

func (s *Server) runCommand(ctx context.Context, in runIn) (*mcp.CallToolResult, error) {
	sess, err := s.get(in.Machine)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Command) == "" {
		return nil, errors.New("command is required")
	}
	var res *mcp.CallToolResult
	err = sess.Do(ctx, session.SourceAgent, func(o *session.Ops) error {
		r, err := remote.RunCommand(o, in.Command, in.Shell, time.Duration(in.Timeout)*time.Second)
		if err != nil {
			if shot, serr := o.Screenshot(session.ShotOptions{}); serr == nil {
				res = &mcp.CallToolResult{IsError: true, Content: shotContent(shot, "Error: "+err.Error()+"\nCurrent screen:")}
				return nil
			}
			return err
		}
		res = textResult(r)
		res.IsError = r.ExitCode != 0
		return nil
	})
	return res, err
}

// ---- file ----

type fileIn struct {
	Machine    string  `json:"machine" jsonschema:"machine name"`
	Action     string  `json:"action" jsonschema:"upload | download | list"`
	LocalPath  string  `json:"local_path,omitempty" jsonschema:"path on the MCP server machine"`
	RemotePath string  `json:"remote_path,omitempty" jsonschema:"Windows path on the remote machine (file, or directory for list)"`
	Content    *string `json:"content,omitempty" jsonschema:"text content to upload instead of local_path"`
	Timeout    int     `json:"timeout,omitempty" jsonschema:"timeout in seconds (default 120)"`
}

func (s *Server) file(ctx context.Context, in fileIn) (*mcp.CallToolResult, error) {
	sess, err := s.get(in.Machine)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(in.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	var res *mcp.CallToolResult
	err = sess.Do(ctx, session.SourceAgent, func(o *session.Ops) error {
		switch strings.ToLower(in.Action) {
		case "upload":
			if in.LocalPath == "" && in.Content == nil {
				return errors.New("upload requires local_path or content")
			}
			r, err := remote.Upload(o, in.LocalPath, in.Content, in.RemotePath, timeout)
			if err != nil {
				return err
			}
			if r.ExitCode != 0 {
				return fmt.Errorf("upload failed: %s", strings.TrimSpace(r.Stderr+" "+r.Stdout))
			}
			res = textResult("ok: uploaded to " + strings.TrimSpace(r.Stdout))
		case "download":
			r, content, err := remote.Download(o, in.RemotePath, in.LocalPath, timeout)
			if err != nil {
				return err
			}
			if r.ExitCode != 0 {
				return fmt.Errorf("download failed: %s", strings.TrimSpace(r.Stderr+" "+r.Stdout))
			}
			if in.LocalPath != "" {
				res = textResult("ok: saved to " + in.LocalPath)
			} else if utf8.Valid(content) {
				res = textResult(string(content))
			} else {
				return errors.New("file is binary; pass local_path to save it")
			}
		case "list":
			entries, err := remote.List(o, in.RemotePath, timeout)
			if err != nil {
				return err
			}
			res = textResult(entries)
		default:
			return fmt.Errorf("action must be upload, download or list")
		}
		return nil
	})
	return res, err
}

// ---- session ----

type sessionIn struct {
	Machine string `json:"machine" jsonschema:"machine name"`
	Action  string `json:"action" jsonschema:"connect | disconnect | reconnect | status | set_resolution | send_ctrl_alt_del"`
	Width   int    `json:"width,omitempty" jsonschema:"width for set_resolution"`
	Height  int    `json:"height,omitempty" jsonschema:"height for set_resolution"`
}

func (s *Server) sessionTool(ctx context.Context, in sessionIn) (*mcp.CallToolResult, error) {
	sess, err := s.get(in.Machine)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(in.Action) {
	case "status":
		return textResult(sess.Info()), nil
	case "connect":
		if err := sess.Connect(ctx); err != nil {
			return nil, err
		}
		return textResult(sess.Info()), nil
	case "disconnect":
		sess.Disconnect()
		return textResult("ok: disconnected"), nil
	case "reconnect":
		if sess.Mode() != session.ModeAgent {
			return nil, &session.ControlError{Machine: sess.Name(), Mode: sess.Mode()}
		}
		if err := sess.Reconnect(ctx); err != nil {
			return nil, err
		}
		return textResult(sess.Info()), nil
	case "set_resolution":
		var res *mcp.CallToolResult
		err := sess.Do(ctx, session.SourceAgent, func(o *session.Ops) error {
			w, h, err := o.Resize(in.Width, in.Height)
			if err != nil {
				return err
			}
			res = textResult(fmt.Sprintf("ok: resolution is now %dx%d; take a new screenshot", w, h))
			return nil
		})
		return res, err
	case "send_ctrl_alt_del":
		var res *mcp.CallToolResult
		err := sess.Do(ctx, session.SourceAgent, func(o *session.Ops) error {
			if err := o.CtrlAltDel(); err != nil {
				return err
			}
			var err error
			res, err = afterAction(o, nil, "ok: sent ctrl+alt+del")
			return err
		})
		return res, err
	}
	return nil, fmt.Errorf("unknown action %q", in.Action)
}

// ---- wait ----

type waitIn struct {
	Machine         string  `json:"machine" jsonschema:"machine name"`
	Seconds         float64 `json:"seconds,omitempty" jsonschema:"seconds to wait (with until_stable: maximum wait, default 10)"`
	UntilStable     bool    `json:"until_stable,omitempty" jsonschema:"wait until the screen stops changing"`
	StableMs        int     `json:"stable_ms,omitempty" jsonschema:"how long the screen must be unchanged (default 500)"`
	ScreenshotAfter *bool   `json:"screenshot_after,omitempty" jsonschema:"return a screenshot (default true)"`
}

func (s *Server) wait(ctx context.Context, in waitIn) (*mcp.CallToolResult, error) {
	sess, err := s.get(in.Machine)
	if err != nil {
		return nil, err
	}
	if in.Seconds > 300 {
		in.Seconds = 300
	}
	var res *mcp.CallToolResult
	err = sess.Do(ctx, session.SourceAdmin, func(o *session.Ops) error {
		note := ""
		if in.UntilStable {
			max := time.Duration(in.Seconds * float64(time.Second))
			if max <= 0 {
				max = 10 * time.Second
			}
			stable := time.Duration(in.StableMs) * time.Millisecond
			if stable <= 0 {
				stable = 500 * time.Millisecond
			}
			ok, err := o.WaitStable(stable, max)
			if err != nil {
				return err
			}
			note = "screen is stable"
			if !ok {
				note = "screen still changing after " + max.String()
			}
		} else {
			d := time.Duration(in.Seconds * float64(time.Second))
			if d <= 0 {
				d = time.Second
			}
			if err := o.Sleep(d); err != nil {
				return err
			}
			note = "waited " + d.String()
		}
		if in.ScreenshotAfter != nil && !*in.ScreenshotAfter {
			res = textResult(note)
			return nil
		}
		shot, err := o.Screenshot(session.ShotOptions{})
		if err != nil {
			return err
		}
		res = &mcp.CallToolResult{Content: shotContent(shot, note)}
		return nil
	})
	return res, err
}
