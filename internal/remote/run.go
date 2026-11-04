package remote

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LianYun/rdp_mcp/internal/imaging"
	"github.com/LianYun/rdp_mcp/internal/session"
)

// Launch failures that leave the desktop untouched carry this prefix so callers can
// tell the model nothing was typed.
var ErrRunDialog = errors.New("the Run dialog did not appear after Win+R (nothing was typed). " +
	"The session may be locked or at the sign-in screen, a full-screen app or UAC prompt may have focus, " +
	"or Win+R may be disabled by policy. Take a screenshot to check; `session reconnect` unlocks a locked session")

// launcher modes, tried in order; the last mode that worked is remembered per machine.
const (
	launchHeadless = iota // conhost --headless: no console window at all
	launchHidden          // powershell -WindowStyle Hidden: may flash a terminal briefly
)

var (
	launchMu   sync.Mutex
	launchPref = map[string]int{}
)

// job describes one prepared command on the share.
type job struct {
	ID      string
	Machine string
	dir     string // local jobs dir
	remote  string // \\tsclient\mcp\jobs\<id>
	Timeout time.Duration
}

func (j *job) local(ext string) string { return filepath.Join(j.dir, j.ID+ext) }

var jobExts = []string{".ps1", ".user.ps1", ".user.cmd", ".out", ".err", ".code", ".pid", ".started"}

func (j *job) cleanup() {
	for _, ext := range jobExts {
		_ = os.Remove(j.local(ext))
	}
}

// prepare writes the user script and the runner for command.
func prepare(s *session.Session, command, shell string, timeout time.Duration) (*job, error) {
	if !s.Resolved().EnableDrive {
		return nil, errors.New("this needs enable_drive: true for the machine (commands run through the \\\\tsclient\\mcp share)")
	}
	dir := filepath.Join(s.ShareDir(), "jobs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	j := &job{ID: newID(), Machine: s.Name(), dir: dir, Timeout: timeout}
	j.remote = remoteRoot + `\jobs\` + j.ID

	var userFile, exe, args string
	switch strings.ToLower(shell) {
	case "", "powershell", "pwsh", "ps":
		userFile = ".user.ps1"
		body := "$ErrorActionPreference = 'Continue'\r\n" +
			"$ProgressPreference = 'SilentlyContinue'\r\n" +
			"[Console]::OutputEncoding = [System.Text.Encoding]::UTF8\r\n" +
			"$OutputEncoding = [System.Text.Encoding]::UTF8\r\n" +
			strings.ReplaceAll(command, "\r\n", "\n") + "\r\n" +
			"if ($LASTEXITCODE) { exit $LASTEXITCODE }\r\n"
		body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")
		if err := os.WriteFile(j.local(userFile), utf8BOM(body), 0o600); err != nil {
			return nil, err
		}
		exe = "powershell.exe"
		args = "'-NoProfile','-NonInteractive','-ExecutionPolicy','Bypass','-File'," + psQuote(j.remote+userFile)
	case "cmd":
		userFile = ".user.cmd"
		body := "@echo off\r\nchcp 65001 >nul\r\n" + strings.ReplaceAll(strings.ReplaceAll(command, "\r\n", "\n"), "\n", "\r\n") + "\r\n"
		if err := os.WriteFile(j.local(userFile), []byte(body), 0o600); err != nil {
			return nil, err
		}
		exe = "cmd.exe"
		args = "'/d','/c'," + psQuote(`"`+j.remote+userFile+`"`)
	default:
		return nil, fmt.Errorf("shell must be powershell or cmd")
	}

	timeoutMs := int(timeout / time.Millisecond)
	runner := "$base = " + psQuote(j.remote) + "\r\n" +
		// Run at most once even if the launcher fallback starts a second copy.
		"try { $m = [System.IO.File]::Open(\"$base.started\", 'CreateNew', 'Write', 'None'); $b = [Text.Encoding]::ASCII.GetBytes([string]$PID); $m.Write($b, 0, $b.Length); $m.Close() } catch { exit 0 }\r\n" +
		"try {\r\n" +
		"  $p = Start-Process -FilePath " + exe + " -ArgumentList " + args +
		" -RedirectStandardOutput \"$base.out\" -RedirectStandardError \"$base.err\" -NoNewWindow -PassThru\r\n" +
		// Touch Handle while the process is alive, otherwise ExitCode is $null after WaitForExit.
		"  $null = $p.Handle\r\n" +
		"  Set-Content -LiteralPath \"$base.pid\" -Value $p.Id -Encoding ASCII\r\n" +
		"  if (-not $p.WaitForExit(" + strconv.Itoa(timeoutMs) + ")) { try { & taskkill.exe /PID $p.Id /T /F | Out-Null } catch {}; $code = 'timeout' } else { $p.WaitForExit(); $code = $p.ExitCode }\r\n" +
		"  if ($null -eq $code) { $code = 'unknown' }\r\n" +
		"} catch { $_ | Out-String | Set-Content -LiteralPath \"$base.err\" -Encoding UTF8; $code = 1 }\r\n" +
		"Set-Content -LiteralPath \"$base.code\" -Value $code -Encoding ASCII\r\n"
	if err := os.WriteFile(j.local(".ps1"), utf8BOM(runner), 0o600); err != nil {
		return nil, err
	}
	return j, nil
}

func (j *job) launchLine(mode int) string {
	ps := `powershell -NoProfile -NonInteractive -ExecutionPolicy Bypass -File ` + j.remote + `.ps1`
	if mode == launchHeadless {
		return `conhost.exe --headless ` + ps
	}
	return `powershell -NoProfile -NonInteractive -WindowStyle Hidden -ExecutionPolicy Bypass -File ` + j.remote + `.ps1`
}

// openRunDialog presses Win+R and verifies that the screen changed in a way
// consistent with the Run dialog opening, so nothing is typed into another window.
func openRunDialog(o *session.Ops) error {
	before, _, err := o.CaptureRGBA(false)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := o.Hotkey([]string{"win", "r"}); err != nil {
			return err
		}
		_ = o.Sleep(400 * time.Millisecond)
		_, _ = o.WaitStable(250*time.Millisecond, 2*time.Second)
		after, _, err := o.CaptureRGBA(false)
		if err != nil {
			return err
		}
		if runDialogAppeared(before, after) {
			return nil
		}
	}
	return ErrRunDialog
}

// runDialogAppeared reports whether a dialog-sized area changed between captures.
// The Run dialog opens at the bottom-left of the primary work area.
func runDialogAppeared(before, after *image.RGBA) bool {
	if before == nil || after == nil || before.Bounds() != after.Bounds() {
		return true // resolution changed; cannot compare, assume it opened
	}
	b := before.Bounds()
	bottomLeft := image.Rect(0, b.Dy()/2, b.Dx()/2, b.Dy())
	return imaging.DiffFraction(before, after, bottomLeft) > 0.004 || imaging.DiffFraction(before, after, b) > 0.003
}

// launch types the launch line into the Run dialog and waits until the runner
// confirms it started (.started file), falling back to the next launcher mode.
func launch(o *session.Ops, j *job) error {
	launchMu.Lock()
	first := launchPref[j.Machine]
	launchMu.Unlock()
	var lastErr error
	for mode := first; mode <= launchHidden; mode++ {
		if err := openRunDialog(o); err != nil {
			return err
		}
		if err := o.Hotkey([]string{"ctrl", "a"}); err != nil {
			return err
		}
		if err := o.Type(j.launchLine(mode)); err != nil {
			return err
		}
		_ = o.Sleep(150 * time.Millisecond)
		if err := o.Press([]string{"enter"}, 1); err != nil {
			return err
		}
		if waitFile(o, j.local(".started"), 10*time.Second) {
			launchMu.Lock()
			launchPref[j.Machine] = mode
			launchMu.Unlock()
			return nil
		}
		lastErr = errors.New("the command was typed into the Run dialog but did not start within 10s " +
			"(drive redirection may be blocked, or an error dialog is showing). Take a screenshot to check")
	}
	return lastErr
}

func waitFile(o *session.Ops, path string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if st, err := os.Stat(path); err == nil && st.Size() > 0 {
			return true
		}
		if o.Sleep(250*time.Millisecond) != nil {
			return false
		}
	}
	return false
}

// result reads the finished job's outcome, or nil if it is still running.
func (j *job) result() *Result {
	b, err := os.ReadFile(j.local(".code"))
	if err != nil || len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	code := strings.TrimSpace(decodeOutput(b))
	res := &Result{Stdout: readCapped(j.local(".out")), Stderr: readCapped(j.local(".err"))}
	switch code {
	case "timeout":
		res.TimedOut, res.ExitCode = true, -1
	case "unknown":
		res.ExitCode = -1
	default:
		res.ExitCode, _ = strconv.Atoi(code)
	}
	return res
}

// RunCommand runs command on the remote machine and returns its output.
// It must be called inside Session.Do.
func RunCommand(o *session.Ops, command, shell string, timeout time.Duration) (*Result, error) {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	j, err := prepare(o.Session(), command, shell, timeout)
	if err != nil {
		return nil, err
	}
	defer j.cleanup()
	if err := launch(o, j); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout + 15*time.Second)
	for {
		if res := j.result(); res != nil {
			return res, nil
		}
		if time.Now().After(deadline) {
			return nil, errors.New("timed out waiting for the command result (the session may have been disconnected)")
		}
		if err := o.Sleep(250 * time.Millisecond); err != nil {
			return nil, err
		}
	}
}
