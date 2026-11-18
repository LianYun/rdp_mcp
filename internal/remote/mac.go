package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/LianYun/rdp_mcp/internal/session"
)

// macOS machines (docs/design.md §5a): commands and files go over SSH. A command runs from a
// small runner script in ~/Library/Caches/remoter/jobs that starts the user's script with a
// login shell in its own process group, enforces the timeout and writes .out/.err/.pid/.code,
// so synchronous and detached commands work the same way and survive SSH disconnects.

const macJobDir = `"$HOME/Library/Caches/remoter/jobs"`

const macRunner = `b="$1"; t="$2"; sh="$3"
cd "$HOME" 2>/dev/null
set -m
"$sh" -l "$b.user" >"$b.out" 2>"$b.err" </dev/null &
p=$!
echo "$p" > "$b.pid"
( sleep "$t"; : > "$b.timedout"; kill -TERM -"$p" 2>/dev/null; sleep 3; kill -KILL -"$p" 2>/dev/null ) >/dev/null 2>&1 &
w=$!
wait "$p"; c=$?
kill -TERM -"$w" 2>/dev/null
[ -e "$b.timedout" ] && c=timeout
echo "$c" > "$b.code.tmp" && mv "$b.code.tmp" "$b.code"
`

// macJob is a job's file set on the Mac.
type macJob struct {
	s    *session.Session
	base string // absolute path without extension
}

func sshCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 20*time.Second)
}

func (m *macJob) read(ext string) ([]byte, error) {
	ctx, cancel := sshCtx()
	defer cancel()
	res, err := m.s.SSHRun(ctx, "cat "+session.ShellQuote(m.base+ext), nil)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, os.ErrNotExist
	}
	return res.Stdout, nil
}

func (m *macJob) tail(ext string, max int) string {
	ctx, cancel := sshCtx()
	defer cancel()
	q := session.ShellQuote(m.base + ext)
	res, err := m.s.SSHRun(ctx, fmt.Sprintf("s=$(stat -f %%z %s 2>/dev/null || echo 0); echo $s; tail -c %d %s 2>/dev/null", q, max, q), nil)
	if err != nil || res.ExitCode != 0 {
		return ""
	}
	sizeLine, body, _ := strings.Cut(string(res.Stdout), "\n")
	size, _ := strconv.Atoi(strings.TrimSpace(sizeLine))
	prefix := ""
	if size > max {
		prefix = "...[earlier output truncated]\n"
	}
	return prefix + strings.TrimRight(body, "\n")
}

func (m *macJob) cleanup() {
	ctx, cancel := sshCtx()
	defer cancel()
	_, _ = m.s.SSHRun(ctx, "rm -f "+session.ShellQuote(m.base)+".*", nil)
}

func macShell(shell string) (string, string, error) {
	switch strings.ToLower(strings.TrimSpace(shell)) {
	case "", "zsh":
		return "/bin/zsh", "zsh", nil
	case "bash":
		return "/bin/bash", "bash", nil
	case "sh":
		return "/bin/sh", "sh", nil
	}
	return "", "", errors.New("shell must be zsh (default), bash or sh on macOS")
}

// macStart uploads the script and runner and starts the job in the background.
func macStart(ctx context.Context, s *session.Session, command, shell string, timeout time.Duration) (*job, string, error) {
	sh, name, err := macShell(shell)
	if err != nil {
		return nil, "", err
	}
	secs := int(timeout / time.Second)
	if secs < 1 {
		secs = 1
	}
	id := newID()
	script := "umask 077; d=" + macJobDir + `; mkdir -p "$d" || exit 1; b="$d/` + id + `"` + "\n" +
		`cat > "$b.user" || exit 1` + "\n" +
		`cat > "$b.run" <<'REMOTER_RUNNER_EOF'` + "\n" + macRunner + "REMOTER_RUNNER_EOF\n" +
		`nohup /bin/sh "$b.run" "$b" ` + strconv.Itoa(secs) + " " + sh + ` >/dev/null 2>&1 </dev/null &` + "\n" +
		`echo "$b"`
	res, err := s.SSHRun(ctx, script, strings.NewReader(command+"\n"))
	if err != nil {
		return nil, "", err
	}
	base := strings.TrimSpace(string(res.Stdout))
	if res.ExitCode != 0 || base == "" {
		return nil, "", fmt.Errorf("could not start the command over SSH: %s", strings.TrimSpace(string(res.Stderr)))
	}
	return &job{ID: id, Machine: s.Name(), mac: &macJob{s: s, base: base}, Timeout: timeout}, name, nil
}

// MacRun runs command on a Mac and waits for its result.
func MacRun(ctx context.Context, s *session.Session, command, shell string, timeout time.Duration) (*Result, error) {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	j, _, err := macStart(ctx, s, command, shell, timeout)
	if err != nil {
		return nil, err
	}
	defer j.cleanup()
	q := session.ShellQuote(j.mac.base)
	wctx, cancel := context.WithTimeout(ctx, timeout+20*time.Second)
	defer cancel()
	// Wait on the Mac for .code to appear (one round trip instead of polling).
	res, err := s.SSHRun(wctx, `while [ ! -s `+q+`.code ]; do sleep 0.2; done; cat `+q+`.code`, nil)
	if err != nil {
		macKill(s, j.mac.base)
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, errors.New("timed out waiting for the command result")
		}
		return nil, err
	}
	out := &Result{Stdout: j.tail(".out", maxOutput), Stderr: j.tail(".err", maxOutput)}
	switch code := strings.TrimSpace(string(res.Stdout)); code {
	case "timeout":
		out.TimedOut, out.ExitCode = true, -1
	default:
		out.ExitCode, _ = strconv.Atoi(code)
	}
	return out, nil
}

// MacDetach starts command on a Mac and returns immediately (job tool follows it).
func MacDetach(ctx context.Context, s *session.Session, command, shell string, timeout time.Duration) (*JobInfo, error) {
	if timeout <= 0 {
		timeout = time.Hour
	}
	j, name, err := macStart(ctx, s, command, shell, timeout)
	if err != nil {
		return nil, err
	}
	return register(s, j, command, name, func() {}), nil
}

func macKill(s *session.Session, base string) {
	ctx, cancel := sshCtx()
	defer cancel()
	q := session.ShellQuote(base)
	_, _ = s.SSHRun(ctx, `p=$(cat `+q+`.pid 2>/dev/null); [ -n "$p" ] && { kill -TERM -"$p" 2>/dev/null; sleep 2; kill -KILL -"$p" 2>/dev/null; }; true`, nil)
}

// JobCancelMac kills a Mac job's process group.
func JobCancelMac(ctx context.Context, s *session.Session, id string) (*JobInfo, error) {
	d, err := getJob(s.Name(), id)
	if err != nil {
		return nil, err
	}
	if d.mac == nil {
		return nil, errors.New("not a macOS job")
	}
	jobsMu.Lock()
	state := d.state
	if state == "running" {
		d.cancelled = true
	}
	jobsMu.Unlock()
	if state != "running" {
		return JobStatus(d.Machine, id)
	}
	macKill(s, d.mac.base)
	for i := 0; i < 20 && !refresh(d); i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return JobStatus(d.Machine, id)
}

// ---- files ----

// macPath assigns the remote path to shell variable p, expanding a leading ~.
func macPath(v, path string) string {
	return v + "=" + session.ShellQuote(path) + `; case "$` + v + `" in "~") ` + v + `="$HOME";; "~/"*) ` + v + `="$HOME/${` + v + `#"~/"}";; esac` + "\n"
}

// MacUpload writes a local file (or content) to remotePath on the Mac.
func MacUpload(ctx context.Context, s *session.Session, localPath string, content *string, remotePath string, appendTo bool) (*Result, error) {
	if remotePath == "" {
		return nil, errors.New("remote_path is required")
	}
	var in io.Reader
	name := "content.txt"
	if content != nil {
		in = strings.NewReader(*content)
	} else {
		f, err := os.Open(localPath)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		in = f
		name = filepath.Base(localPath)
	}
	op := ">"
	if appendTo {
		op = ">>"
	}
	script := macPath("p", remotePath) +
		`if [ -d "$p" ] || { [ "${p%/}" != "$p" ] && [ ` + strconv.Quote(op) + ` = ">" ]; }; then mkdir -p "$p" && p="${p%/}/"` + session.ShellQuote(name) + `; else mkdir -p "$(dirname "$p")"; fi || exit 1` + "\n" +
		`cat ` + op + ` "$p" && echo "$p"`
	res, err := s.SSHRun(ctx, script, in)
	if err != nil {
		return nil, err
	}
	return &Result{ExitCode: res.ExitCode, Stdout: strings.TrimSpace(string(res.Stdout)), Stderr: strings.TrimSpace(string(res.Stderr))}, nil
}

// MacDownload reads remotePath; with localPath empty the content (<=1MB) is returned.
func MacDownload(ctx context.Context, s *session.Session, remotePath, localPath string) ([]byte, error) {
	if remotePath == "" {
		return nil, errors.New("remote_path is required")
	}
	pre := macPath("p", remotePath) + `if [ -d "$p" ]; then echo "is a directory (directories are not supported yet)" >&2; exit 2; fi` + "\n"
	if localPath == "" {
		res, err := s.SSHRun(ctx, pre+`head -c 1048577 "$p"`, nil)
		if err != nil {
			return nil, err
		}
		if res.ExitCode != 0 {
			return nil, fmt.Errorf("download failed: %s", strings.TrimSpace(string(res.Stderr)))
		}
		if len(res.Stdout) > 1<<20 {
			return nil, errors.New("file is larger than 1MB; pass local_path to save it to disk")
		}
		return res.Stdout, nil
	}
	if st, err := os.Stat(localPath); (err == nil && st.IsDir()) || strings.HasSuffix(localPath, string(os.PathSeparator)) {
		localPath = filepath.Join(localPath, filepath.Base(strings.TrimRight(remotePath, "/")))
	}
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return nil, err
	}
	tmp := localPath + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	res, err := s.SSHStream(ctx, pre+`cat "$p"`, nil, f)
	cerr := f.Close()
	if err == nil && res.ExitCode != 0 {
		err = fmt.Errorf("download failed: %s", strings.TrimSpace(string(res.Stderr)))
	}
	if err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	return nil, os.Rename(tmp, localPath)
}

const macStatFmt = `stat -f '%N%t%HT%t%z%t%Sm' -t '%Y-%m-%dT%H:%M:%S'`

type macStat struct {
	path, typ, mtime string
	size             int64
}

func parseMacStat(out string) []macStat {
	var r []macStat
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 4 {
			continue
		}
		typ := "file"
		switch f[1] {
		case "Directory":
			typ = "dir"
		case "Symbolic Link":
			typ = "link"
		}
		size, _ := strconv.ParseInt(f[2], 10, 64)
		if typ == "dir" {
			size = 0
		}
		r = append(r, macStat{path: f[0], typ: typ, size: size, mtime: f[3]})
	}
	return r
}

// MacList lists a directory on the Mac.
func MacList(ctx context.Context, s *session.Session, remotePath string) ([]FileEntry, error) {
	if remotePath == "" {
		return nil, errors.New("remote_path is required")
	}
	res, err := s.SSHRun(ctx, macPath("p", remotePath)+
		`[ -d "$p" ] || { echo "not a directory: $p" >&2; exit 2; }`+"\n"+
		`find "$p/" -mindepth 1 -maxdepth 1 -exec `+macStatFmt+` {} +`, nil)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("list failed: %s", strings.TrimSpace(string(res.Stderr)))
	}
	entries := []FileEntry{}
	for _, st := range parseMacStat(string(res.Stdout)) {
		entries = append(entries, FileEntry{Name: filepath.Base(st.path), Type: st.typ, Size: st.size, LastWriteTime: st.mtime})
	}
	return entries, nil
}

// MacSearch finds names matching pattern under root, recursively.
func MacSearch(ctx context.Context, s *session.Session, root, pattern string, limit int) ([]SearchEntry, bool, error) {
	if root == "" {
		return nil, false, errors.New("remote_path (the directory to search) is required")
	}
	if pattern == "" {
		pattern = "*"
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	res, err := s.SSHRun(ctx, macPath("p", root)+
		`[ -d "$p" ] || { echo "not a directory: $p" >&2; exit 2; }`+"\n"+
		`find "$p/" -mindepth 1 -name `+session.ShellQuote(pattern)+` -exec `+macStatFmt+` {} + 2>/dev/null | head -n `+strconv.Itoa(limit+1), nil)
	if err != nil {
		return nil, false, err
	}
	if res.ExitCode != 0 && len(res.Stdout) == 0 {
		return nil, false, fmt.Errorf("search failed: %s", strings.TrimSpace(string(res.Stderr)))
	}
	sts := parseMacStat(string(res.Stdout))
	truncated := len(sts) > limit
	if truncated {
		sts = sts[:limit]
	}
	entries := []SearchEntry{}
	for _, st := range sts {
		entries = append(entries, SearchEntry{Path: strings.ReplaceAll(st.path, "//", "/"), Type: st.typ, Size: st.size, LastWriteTime: st.mtime})
	}
	return entries, truncated, nil
}
