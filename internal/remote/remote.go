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
	return strings.TrimRight(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}

// Upload copies a local file (or literal content) to remotePath on the machine.
// With appendTo set, the bytes are appended to remotePath (created if missing) instead of replacing it.
func Upload(o *session.Ops, localPath string, content *string, remotePath string, appendTo bool, timeout time.Duration) (*Result, error) {
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
	if appendTo {
		cmd := "$dst = " + psQuote(remotePath) + "\r\n" +
			"$parent = Split-Path -Parent $dst; if ($parent) { New-Item -ItemType Directory -Force -Path $parent | Out-Null }\r\n" +
			"$bytes = [System.IO.File]::ReadAllBytes(" + psQuote(src) + ")\r\n" +
			"$f = [System.IO.File]::Open($dst, 'Append', 'Write'); try { $f.Write($bytes, 0, $bytes.Length) } finally { $f.Close() }\r\n" +
			"Write-Output (Get-Item -LiteralPath $dst).FullName\r\n"
		return RunCommand(o, cmd, "powershell", timeout)
	}
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

// SearchEntry is one match of Search.
type SearchEntry struct {
	Path          string `json:"path"`
	Type          string `json:"type"`
	Size          int64  `json:"size"`
	LastWriteTime string `json:"last_write_time"`
}

// Search finds files and directories under root whose name matches pattern
// (wildcards * and ?), recursively, returning at most limit entries.
func Search(o *session.Ops, root, pattern string, limit int, timeout time.Duration) ([]SearchEntry, bool, error) {
	if root == "" {
		return nil, false, errors.New("remote_path (the directory to search) is required")
	}
	if pattern == "" {
		pattern = "*"
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	n := fmt.Sprint(limit)
	cmd := "$items = @(Get-ChildItem -Force -Recurse -LiteralPath " + psQuote(root) + " -Filter " + psQuote(pattern) +
		" -ErrorAction SilentlyContinue | Select-Object -First " + fmt.Sprint(limit+1) + ")\r\n" +
		"$out = @($items | Select-Object -First " + n + " | ForEach-Object { [pscustomobject]@{ path = $_.FullName; " +
		"type = $(if ($_.PSIsContainer) {'dir'} else {'file'}); size = $(if ($_.PSIsContainer) {0} else {$_.Length}); " +
		"last_write_time = $_.LastWriteTime.ToString('s') } })\r\n" +
		"ConvertTo-Json -Compress -Depth 3 -InputObject ([pscustomobject]@{ truncated = ($items.Count -gt " + n + "); entries = $out })\r\n"
	res, err := RunCommand(o, cmd, "powershell", timeout)
	if err != nil {
		return nil, false, err
	}
	if res.ExitCode != 0 {
		return nil, false, fmt.Errorf("search failed: %s", strings.TrimSpace(res.Stderr+" "+res.Stdout))
	}
	return parseSearch(res.Stdout)
}

func parseSearch(stdout string) ([]SearchEntry, bool, error) {
	var r struct {
		Truncated bool            `json:"truncated"`
		Entries   json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &r); err != nil {
		return nil, false, fmt.Errorf("parse search result: %w", err)
	}
	entries := []SearchEntry{}
	raw := strings.TrimSpace(string(r.Entries))
	switch {
	case raw == "" || raw == "null":
	case strings.HasPrefix(raw, "{"): // PowerShell 5 unwraps single-element arrays
		var e SearchEntry
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			return nil, false, err
		}
		entries = append(entries, e)
	default:
		if err := json.Unmarshal([]byte(raw), &entries); err != nil {
			return nil, false, err
		}
	}
	return entries, r.Truncated, nil
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
