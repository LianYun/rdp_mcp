package remote

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LianYun/rdp_mcp/internal/config"
	"github.com/LianYun/rdp_mcp/internal/events"
	"github.com/LianYun/rdp_mcp/internal/session"
	"github.com/LianYun/rdp_mcp/internal/testutil/fakeworker"
)

func TestMain(m *testing.M) {
	fakeworker.MaybeRun()
	os.Exit(m.Run())
}

func newSession(t *testing.T) (*session.Session, string) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "input.jsonl")
	for _, kv := range fakeworker.Env(logPath) {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	cfg, err := config.Parse([]byte("machines:\n  - name: m1\n    host: 127.0.0.1\n    username: u\n    password: p\n"), filepath.Join(dir, "c.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	m := session.NewManager(cfg, session.Options{WorkerPath: exe, DataDir: dir, Bus: events.NewBus()})
	t.Cleanup(m.Shutdown)
	s, err := m.Get("m1")
	if err != nil {
		t.Fatal(err)
	}
	return s, logPath
}

// fakeWindows plays the Windows side of the share: when a runner script appears it
// "starts" it and, after delay, writes the given output and exit code.
func fakeWindows(t *testing.T, s *session.Session, stdout string, code string, delay time.Duration) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	dir := filepath.Join(s.ShareDir(), "jobs")
	go func() {
		seen := map[string]bool{}
		for ctx.Err() == nil {
			matches, _ := filepath.Glob(filepath.Join(dir, "*.ps1"))
			for _, m := range matches {
				if strings.HasSuffix(m, ".user.ps1") || seen[m] {
					continue
				}
				seen[m] = true
				base := strings.TrimSuffix(m, ".ps1")
				go func() {
					time.Sleep(300 * time.Millisecond) // typed + Enter
					_ = os.WriteFile(base+".started", []byte("1234"), 0o600)
					_ = os.WriteFile(base+".pid", []byte("4321"), 0o600)
					select {
					case <-time.After(delay):
					case <-ctx.Done():
						return
					}
					_ = os.WriteFile(base+".out", append([]byte{0xEF, 0xBB, 0xBF}, stdout...), 0o600)
					_ = os.WriteFile(base+".err", nil, 0o600)
					_ = os.WriteFile(base+".code", []byte(code), 0o600)
				}()
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	return cancel
}

func TestRunCommandRoundTrip(t *testing.T) {
	s, logPath := newSession(t)
	defer fakeWindows(t, s, "host1\r\nline2\r\n", "3", 200*time.Millisecond)()
	var res *Result
	err := s.Do(context.Background(), session.SourceAgent, func(o *session.Ops) error {
		var e error
		res, e = RunCommand(o, "hostname", "", 10*time.Second)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 3 || res.Stdout != "host1\nline2" {
		t.Fatalf("%+v", res)
	}
	// the typed launch line uses the headless console and the job's runner script
	log, _ := os.ReadFile(logPath)
	if !strings.Contains(string(log), "conhost.exe --headless") {
		t.Fatalf("launch line not typed: %s", log)
	}
	if left, _ := filepath.Glob(filepath.Join(s.ShareDir(), "jobs", "*")); len(left) != 0 {
		t.Fatalf("job files not cleaned up: %v", left)
	}
}

func TestRunDialogMissingTypesNothing(t *testing.T) {
	t.Setenv("FAKE_WORKER_NO_RUN_DIALOG", "1")
	s, logPath := newSession(t)
	err := s.Do(context.Background(), session.SourceAgent, func(o *session.Ops) error {
		_, e := RunCommand(o, "hostname", "", 5*time.Second)
		return e
	})
	if !errors.Is(err, ErrRunDialog) {
		t.Fatalf("want ErrRunDialog, got %v", err)
	}
	if log, _ := os.ReadFile(logPath); strings.Contains(string(log), `"unicode"`) {
		t.Fatal("text was typed although the Run dialog never appeared")
	}
}

func TestDetachedJob(t *testing.T) {
	s, _ := newSession(t)
	defer fakeWindows(t, s, "done\n", "0", 1500*time.Millisecond)()
	var info *JobInfo
	err := s.Do(context.Background(), session.SourceAgent, func(o *session.Ops) error {
		var e error
		info, e = Detach(o, "Start-Sleep 1", "", time.Minute)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.State != "running" || info.PID != 4321 {
		t.Fatalf("%+v", info)
	}
	if err := JobRemove("m1", info.ID); err == nil {
		t.Fatal("removed a running job")
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		st, err := JobStatus("m1", info.ID)
		if err != nil {
			t.Fatal(err)
		}
		if st.State == "done" {
			out, _ := JobOutputTail("m1", info.ID, 0)
			if out.Stdout != "done" || *out.ExitCode != 0 {
				t.Fatalf("%+v", out)
			}
			if len(JobList("m1")) != 1 {
				t.Fatal("job list")
			}
			if err := JobRemove("m1", info.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := JobStatus("m1", info.ID); err == nil {
				t.Fatal("job not removed")
			}
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("job did not finish")
}

func TestReadTailAndDecode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "o")
	_ = os.WriteFile(p, []byte(strings.Repeat("a", 100)+"END\r\n"), 0o600)
	got := readTail(p, 10)
	if !strings.HasPrefix(got, "...[earlier output truncated]") || !strings.HasSuffix(got, "END") {
		t.Fatal(got)
	}
	if decodeOutput([]byte{0xFF, 0xFE, 'h', 0, 'i', 0}) != "hi" {
		t.Fatal("utf16")
	}
	if psQuote("it's") != "'it''s'" {
		t.Fatal(psQuote("it's"))
	}
}
