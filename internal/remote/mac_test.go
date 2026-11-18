package remote

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/LianYun/rdp_mcp/internal/config"
	"github.com/LianYun/rdp_mcp/internal/events"
	"github.com/LianYun/rdp_mcp/internal/session"
	"github.com/LianYun/rdp_mcp/internal/testutil/fakessh"
)

// newMacSession returns a macOS session whose SSH goes to a fake server running commands locally.
func newMacSession(t *testing.T) (*session.Session, string) {
	t.Helper()
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	port := fakessh.Start(t, "u", "p", home)
	yaml := fmt.Sprintf("machines:\n  - name: mac\n    protocol: vnc\n    host: 127.0.0.1\n    ssh_port: %d\n    username: u\n    password: p\n", port)
	cfg, err := config.Parse([]byte(yaml), filepath.Join(dir, "c.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	m := session.NewManager(cfg, session.Options{DataDir: dir, Bus: events.NewBus()})
	t.Cleanup(m.Shutdown)
	s, err := m.Get("mac")
	if err != nil {
		t.Fatal(err)
	}
	return s, home
}

func TestMacRun(t *testing.T) {
	s, home := newMacSession(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := MacRun(ctx, s, "echo 'hi it'\"'\"'s 中文'; echo oops >&2; exit 7", "sh", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 7 || res.Stdout != "hi it's 中文" || res.Stderr != "oops" {
		t.Fatalf("result %+v", res)
	}
	// Job files are cleaned up after a synchronous run.
	left, _ := filepath.Glob(filepath.Join(home, "Library/Caches/remoter/jobs/*"))
	if len(left) != 0 {
		t.Fatalf("leftover job files: %v", left)
	}

	res, err = MacRun(ctx, s, "sleep 20", "sh", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut || res.ExitCode != -1 {
		t.Fatalf("timeout result %+v", res)
	}
	if _, err := MacRun(ctx, s, "true", "fish", time.Second); err == nil {
		t.Fatal("unknown shell accepted")
	}
}

func TestMacDetachAndCancel(t *testing.T) {
	s, _ := newMacSession(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	j, err := MacDetach(ctx, s, "echo started; sleep 30; echo never", "sh", 0)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != "running" || j.Shell != "sh" {
		t.Fatalf("job %+v", j)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		out, err := JobOutputTail("mac", j.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		if out.Stdout == "started" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no output: %+v", out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	got, err := JobCancelMac(ctx, s, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "cancelled" {
		t.Fatalf("after cancel: %+v", got)
	}
	if err := JobRemove("mac", j.ID); err != nil {
		t.Fatal(err)
	}

	j, err = MacDetach(ctx, s, "exit 3", "sh", 0)
	if err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(10 * time.Second)
	for {
		st, _ := JobStatus("mac", j.ID)
		if st.State == "done" {
			if st.ExitCode == nil || *st.ExitCode != 3 {
				t.Fatalf("exit code %+v", st)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not finish: %+v", st)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestMacFiles(t *testing.T) {
	s, home := newMacSession(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	content := "第一行 it's\n"
	r, err := MacUpload(ctx, s, "", &content, "~/up/a b/x.txt", false)
	if err != nil || r.ExitCode != 0 {
		t.Fatalf("upload: %v %+v", err, r)
	}
	if r.Stdout != filepath.Join(home, "up/a b/x.txt") {
		t.Fatalf("uploaded to %q", r.Stdout)
	}
	more := "second\n"
	if r, err := MacUpload(ctx, s, "", &more, "~/up/a b/x.txt", true); err != nil || r.ExitCode != 0 {
		t.Fatalf("append: %v %+v", err, r)
	}
	got, err := MacDownload(ctx, s, "~/up/a b/x.txt", "")
	if err != nil || string(got) != content+more {
		t.Fatalf("download: %v %q", err, got)
	}

	// Binary round trip through local files; a trailing slash means "into this directory".
	src := filepath.Join(t.TempDir(), "bin.dat")
	data := make([]byte, 200000)
	for i := range data {
		data[i] = byte(i * 7)
	}
	os.WriteFile(src, data, 0o600)
	if r, err := MacUpload(ctx, s, src, nil, "~/up/bin/", false); err != nil || r.ExitCode != 0 {
		t.Fatalf("upload file: %v %+v", err, r)
	}
	dst := filepath.Join(t.TempDir(), "back") + "/"
	if _, err := MacDownload(ctx, s, "~/up/bin/bin.dat", dst); err != nil {
		t.Fatal(err)
	}
	back, _ := os.ReadFile(filepath.Join(dst, "bin.dat"))
	if string(back) != string(data) {
		t.Fatal("binary round trip mismatch")
	}
	if _, err := MacDownload(ctx, s, "~/up", ""); err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("directory download: %v", err)
	}

	if runtime.GOOS != "darwin" {
		t.Skip("list/search use BSD stat")
	}
	entries, err := MacList(ctx, s, "~/up")
	if err != nil || len(entries) != 2 {
		t.Fatalf("list: %v %+v", err, entries)
	}
	found, truncated, err := MacSearch(ctx, s, "~", "*.dat", 10)
	if err != nil || truncated || len(found) != 1 || found[0].Size != 200000 || !strings.HasSuffix(found[0].Path, "/up/bin/bin.dat") {
		t.Fatalf("search: %v %v %+v", err, truncated, found)
	}
}
