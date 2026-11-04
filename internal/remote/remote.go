// Package remote implements run_command and file transfer over the \\tsclient\mcp drive mapping
// (docs/design.md §6.6).
package remote

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/LianYun/rdp_mcp/internal/session"
)

const (
	ShareName  = "mcp"
	remoteRoot = `\\tsclient\` + ShareName
)

type Result struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	TimedOut bool   `json:"timed_out,omitempty"`
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return time.Now().Format("150405") + "-" + hex.EncodeToString(b)
}

// psQuote quotes s as a PowerShell single-quoted string.
func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func utf8BOM(s string) []byte { return append([]byte{0xEF, 0xBB, 0xBF}, []byte(s)...) }

// decodeOutput decodes a captured stream (UTF-8, UTF-8 BOM or UTF-16LE BOM).
func decodeOutput(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		return string(b[3:])
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}):
		b = b[2:]
		u := make([]uint16, len(b)/2)
		for i := range u {
			u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
		}
		return string(utf16.Decode(u))
	}
	return string(b)
}

const maxOutput = 256 << 10

func readCapped(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	b, _ := io.ReadAll(io.LimitReader(f, maxOutput+1))
	s := decodeOutput(b)
	if len(b) > maxOutput {
		s += "\n...[output truncated]"
	}
	return strings.TrimRight(s, "\r\n")
}

// RunCommand runs command on the remote machine and returns its output.
// It must be called inside Session.Do.
func RunCommand(o *session.Ops, command, shell string, timeout time.Duration) (*Result, error) {
	s := o.Session()
	if !s.Resolved().EnableDrive {
		return nil, errors.New("run_command requires enable_drive: true for this machine")
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	jobs := filepath.Join(s.ShareDir(), "jobs")
	if err := os.MkdirAll(jobs, 0o700); err != nil {
		return nil, err
	}
	id := newID()
	local := func(ext string) string { return filepath.Join(jobs, id+ext) }
	remote := remoteRoot + `\jobs\` + id
	defer func() {
		for _, ext := range []string{".ps1", ".user.ps1", ".user.cmd", ".out", ".err", ".code"} {
			_ = os.Remove(local(ext))
		}
	}()

	var userFile, exe, args string
	switch strings.ToLower(shell) {
	case "", "powershell", "pwsh", "ps":
		userFile = ".user.ps1"
		body := "$ErrorActionPreference = 'Continue'\r\n" +
			"$ProgressPreference = 'SilentlyContinue'\r\n" +
			"[Console]::OutputEncoding = [System.Text.Encoding]::UTF8\r\n" +
			"$OutputEncoding = [System.Text.Encoding]::UTF8\r\n" +
			command + "\r\n" +
			"if ($LASTEXITCODE) { exit $LASTEXITCODE }\r\n"
		if err := os.WriteFile(local(userFile), utf8BOM(body), 0o600); err != nil {
			return nil, err
		}
		exe = "powershell.exe"
		args = "'-NoProfile','-NonInteractive','-ExecutionPolicy','Bypass','-File'," + psQuote(remote+userFile)
	case "cmd":
		userFile = ".user.cmd"
		body := "@echo off\r\nchcp 65001 >nul\r\n" + strings.ReplaceAll(command, "\n", "\r\n") + "\r\n"
		if err := os.WriteFile(local(userFile), []byte(body), 0o600); err != nil {
			return nil, err
		}
		exe = "cmd.exe"
		args = "'/d','/c'," + psQuote(`"`+remote+userFile+`"`)
	default:
		return nil, fmt.Errorf("shell must be powershell or cmd")
	}

	timeoutMs := int(timeout / time.Millisecond)
	runner := "$base = " + psQuote(remote) + "\r\n" +
		"try {\r\n" +
		"  $p = Start-Process -FilePath " + exe + " -ArgumentList " + args +
		" -RedirectStandardOutput \"$base.out\" -RedirectStandardError \"$base.err\" -NoNewWindow -PassThru\r\n" +
		"  if (-not $p.WaitForExit(" + strconv.Itoa(timeoutMs) + ")) { try { $p.Kill() } catch {}; $code = 'timeout' } else { $code = $p.ExitCode }\r\n" +
		"} catch { $_ | Out-String | Set-Content -LiteralPath \"$base.err\" -Encoding UTF8; $code = 1 }\r\n" +
		"Set-Content -LiteralPath \"$base.code\" -Value $code -Encoding ASCII\r\n"
	if err := os.WriteFile(local(".ps1"), utf8BOM(runner), 0o600); err != nil {
		return nil, err
	}

	// Launch through the Run dialog.
	launch := `powershell -NoProfile -NonInteractive -WindowStyle Hidden -ExecutionPolicy Bypass -File ` + remote + `.ps1`
	if err := o.Hotkey([]string{"win", "r"}); err != nil {
		return nil, err
	}
	if err := o.Sleep(700 * time.Millisecond); err != nil {
		return nil, err
	}
	if err := o.Hotkey([]string{"ctrl", "a"}); err != nil {
		return nil, err
	}
	if err := o.Type(launch); err != nil {
		return nil, err
	}
	if err := o.Sleep(150 * time.Millisecond); err != nil {
		return nil, err
	}
	if err := o.Press([]string{"enter"}, 1); err != nil {
		return nil, err
	}

	// Wait for the result. Give the launcher extra time beyond the command timeout.
	started := time.Now()
	deadline := started.Add(timeout + 20*time.Second)
	for {
		if b, err := os.ReadFile(local(".code")); err == nil && len(bytes.TrimSpace(b)) > 0 {
			code := strings.TrimSpace(decodeOutput(b))
			res := &Result{Stdout: readCapped(local(".out")), Stderr: readCapped(local(".err"))}
			if code == "timeout" {
				res.TimedOut, res.ExitCode = true, -1
			} else {
				res.ExitCode, _ = strconv.Atoi(code)
			}
			return res, nil
		}
		if time.Now().After(deadline) {
			hint := "the command did not report back. Possible causes: the session is locked or on the login screen, " +
				"a dialog has focus, the Run dialog is disabled by policy, or drive redirection is blocked on the server"
			if time.Since(started) < 15*time.Second+timeout {
				hint = "timed out"
			}
			return nil, errors.New(hint)
		}
		if err := o.Sleep(300 * time.Millisecond); err != nil {
			return nil, err
		}
	}
}

// Upload copies a local file (or literal content) to remotePath on the machine.
func Upload(o *session.Ops, localPath string, content *string, remotePath string, timeout time.Duration) (*Result, error) {
	if remotePath == "" {
		return nil, errors.New("remote_path is required")
	}
	id := newID()
	dir := filepath.Join(o.Session().ShareDir(), "files", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	name := "content.txt"
	if localPath != "" {
		name = filepath.Base(localPath)
	}
	dst := filepath.Join(dir, name)
	if content != nil {
		if err := os.WriteFile(dst, []byte(*content), 0o600); err != nil {
			return nil, err
		}
	} else {
		if err := copyFile(localPath, dst); err != nil {
			return nil, err
		}
	}
	src := remoteRoot + `\files\` + id + `\` + name
	cmd := "$dst = " + psQuote(remotePath) + "\r\n" +
		"if ((Test-Path -LiteralPath $dst -PathType Container) -or $dst.EndsWith('\\')) { New-Item -ItemType Directory -Force -Path $dst | Out-Null; $dst = Join-Path $dst " + psQuote(name) + " } " +
		"else { $parent = Split-Path -Parent $dst; if ($parent) { New-Item -ItemType Directory -Force -Path $parent | Out-Null } }\r\n" +
		"Copy-Item -LiteralPath " + psQuote(src) + " -Destination $dst -Force -ErrorAction Stop\r\n" +
		"Write-Output (Get-Item -LiteralPath $dst).FullName\r\n"
	return RunCommand(o, cmd, "powershell", timeout)
}

// Download copies remotePath to localPath. If localPath is empty, the content is returned (text, <=1MB).
func Download(o *session.Ops, remotePath, localPath string, timeout time.Duration) (res *Result, content []byte, err error) {
	if remotePath == "" {
		return nil, nil, errors.New("remote_path is required")
	}
	id := newID()
	dir := filepath.Join(o.Session().ShareDir(), "files", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(dir)
	cmd := "Copy-Item -LiteralPath " + psQuote(remotePath) + " -Destination " +
		psQuote(remoteRoot+`\files\`+id+`\`) + " -Force -ErrorAction Stop\r\n"
	res, err = RunCommand(o, cmd, "powershell", timeout)
	if err != nil || res.ExitCode != 0 {
		return res, nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].IsDir() {
		return res, nil, errors.New("download expects a single file (directories are not supported yet)")
	}
	got := filepath.Join(dir, entries[0].Name())
	if localPath == "" {
		st, _ := os.Stat(got)
		if st != nil && st.Size() > 1<<20 {
			return res, nil, errors.New("file is larger than 1MB; pass local_path to save it to disk")
		}
		b, err := os.ReadFile(got)
		return res, b, err
	}
	if st, err := os.Stat(localPath); err == nil && st.IsDir() {
		localPath = filepath.Join(localPath, entries[0].Name())
	}
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return res, nil, err
	}
	return res, nil, copyFile(got, localPath)
}

// FileEntry is one item returned by List.
type FileEntry struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	Size          int64  `json:"size"`
	LastWriteTime string `json:"last_write_time"`
}

// List lists a remote directory.
func List(o *session.Ops, remotePath string, timeout time.Duration) ([]FileEntry, error) {
	if remotePath == "" {
		return nil, errors.New("remote_path is required")
	}
	cmd := "Get-ChildItem -Force -LiteralPath " + psQuote(remotePath) + " -ErrorAction Stop | ForEach-Object { " +
		"[pscustomobject]@{ name = $_.Name; type = $(if ($_.PSIsContainer) {'dir'} else {'file'}); " +
		"size = $(if ($_.PSIsContainer) {0} else {$_.Length}); last_write_time = $_.LastWriteTime.ToString('s') } } | " +
		"ConvertTo-Json -Compress -Depth 2"
	res, err := RunCommand(o, cmd, "powershell", timeout)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("list failed: %s", strings.TrimSpace(res.Stderr+" "+res.Stdout))
	}
	out := strings.TrimSpace(res.Stdout)
	if out == "" {
		return []FileEntry{}, nil
	}
	if strings.HasPrefix(out, "{") {
		out = "[" + out + "]"
	}
	var entries []FileEntry
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		return nil, fmt.Errorf("parse listing: %w", err)
	}
	return entries, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
