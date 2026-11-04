package remote

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LianYun/rdp_mcp/internal/session"
)

// Detached jobs (docs/design.md D19): run_command detach:true starts the command and
// returns immediately; the job tool queries, reads output and cancels.

const maxJobsPerMachine = 50

// JobInfo is the public view of a detached job.
type JobInfo struct {
	ID        string     `json:"id"`
	Machine   string     `json:"machine"`
	Command   string     `json:"command"`
	Shell     string     `json:"shell"`
	State     string     `json:"state"` // running | done | timeout | cancelled | lost
	ExitCode  *int       `json:"exit_code,omitempty"`
	PID       int        `json:"pid,omitempty"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	Elapsed   string     `json:"elapsed"`
	Timeout   string     `json:"timeout"`
}

type detached struct {
	*job
	command   string
	shell     string
	started   time.Time
	ended     time.Time
	state     string
	exitCode  *int
	cancelled bool
}

var (
	jobsMu sync.Mutex
	jobs   = map[string]*detached{} // id -> job
)

// Detach starts command and returns without waiting. Call inside Session.Do.
func Detach(o *session.Ops, command, shell string, timeout time.Duration) (*JobInfo, error) {
	if timeout <= 0 {
		timeout = time.Hour
	}
	s := o.Session()
	j, err := prepare(s, command, shell, timeout)
	if err != nil {
		return nil, err
	}
	if err := launch(o, j); err != nil {
		j.cleanup()
		return nil, err
	}
	d := &detached{job: j, command: command, shell: shellName(shell), started: time.Now(), state: "running"}
	jobsMu.Lock()
	jobs[j.ID] = d
	pruneLocked(j.Machine)
	jobsMu.Unlock()

	release := s.HoldIdle()
	go watch(s, d, release)
	info := d.info()
	return &info, nil
}

func shellName(s string) string {
	if strings.EqualFold(s, "cmd") {
		return "cmd"
	}
	return "powershell"
}

// watch polls for completion, then releases the idle hold and publishes a job event.
func watch(s *session.Session, d *detached, release func()) {
	defer release()
	deadline := d.started.Add(d.Timeout + time.Minute)
	for {
		time.Sleep(time.Second)
		jobsMu.Lock()
		_, alive := jobs[d.ID]
		jobsMu.Unlock()
		if !alive {
			return
		}
		if refresh(d) {
			info := d.info()
			s.Publish("job", map[string]any{"id": info.ID, "state": info.State, "exit_code": info.ExitCode})
			return
		}
		if time.Now().After(deadline) {
			jobsMu.Lock()
			d.state, d.ended = "lost", time.Now()
			jobsMu.Unlock()
			s.Publish("job", map[string]any{"id": d.ID, "state": "lost"})
			return
		}
	}
}

// refresh updates d from its files; reports whether the job has finished.
func refresh(d *detached) bool {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	if d.state != "running" {
		return true
	}
	b, err := os.ReadFile(d.local(".code"))
	if err != nil || len(bytes.TrimSpace(b)) == 0 {
		return false
	}
	code := strings.TrimSpace(decodeOutput(b))
	d.ended = time.Now()
	switch {
	case d.cancelled:
		d.state = "cancelled"
	case code == "timeout":
		d.state = "timeout"
	default:
		d.state = "done"
	}
	if n, err := strconv.Atoi(code); err == nil {
		d.exitCode = &n
	}
	return true
}

func (d *detached) pid() int {
	b, err := os.ReadFile(d.local(".pid"))
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(decodeOutput(b)))
	return n
}

func (d *detached) info() JobInfo {
	in := JobInfo{ID: d.ID, Machine: d.Machine, Command: d.command, Shell: d.shell, State: d.state,
		ExitCode: d.exitCode, PID: d.pid(), StartedAt: d.started, Timeout: d.Timeout.String()}
	end := time.Now()
	if !d.ended.IsZero() {
		e := d.ended
		in.EndedAt = &e
		end = e
	}
	in.Elapsed = end.Sub(d.started).Round(time.Second).String()
	if len(in.Command) > 200 {
		in.Command = in.Command[:200] + "..."
	}
	return in
}

func pruneLocked(machine string) {
	var mine []*detached
	for _, d := range jobs {
		if d.Machine == machine {
			mine = append(mine, d)
		}
	}
	if len(mine) <= maxJobsPerMachine {
		return
	}
	sort.Slice(mine, func(i, k int) bool { return mine[i].started.Before(mine[k].started) })
	for _, d := range mine[:len(mine)-maxJobsPerMachine] {
		if d.state != "running" {
			d.cleanup()
			delete(jobs, d.ID)
		}
	}
}

func getJob(machine, id string) (*detached, error) {
	jobsMu.Lock()
	d, ok := jobs[id]
	jobsMu.Unlock()
	if !ok || d.Machine != machine {
		return nil, fmt.Errorf("unknown job %q on %s (see job action list)", id, machine)
	}
	refresh(d)
	return d, nil
}

// JobStatus returns the job's state.
func JobStatus(machine, id string) (*JobInfo, error) {
	d, err := getJob(machine, id)
	if err != nil {
		return nil, err
	}
	jobsMu.Lock()
	defer jobsMu.Unlock()
	in := d.info()
	return &in, nil
}

// JobList lists the jobs of a machine, newest first.
func JobList(machine string) []JobInfo {
	jobsMu.Lock()
	var ds []*detached
	for _, d := range jobs {
		if d.Machine == machine {
			ds = append(ds, d)
		}
	}
	jobsMu.Unlock()
	out := []JobInfo{}
	for _, d := range ds {
		refresh(d)
		jobsMu.Lock()
		out = append(out, d.info())
		jobsMu.Unlock()
	}
	sort.Slice(out, func(i, k int) bool { return out[i].StartedAt.After(out[k].StartedAt) })
	return out
}

// JobOutput returns the tail (up to maxBytes, default 64KB) of stdout and stderr.
type JobOutput struct {
	JobInfo
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}

func JobOutputTail(machine, id string, maxBytes int) (*JobOutput, error) {
	d, err := getJob(machine, id)
	if err != nil {
		return nil, err
	}
	if maxBytes <= 0 {
		maxBytes = 64 << 10
	}
	jobsMu.Lock()
	out := &JobOutput{JobInfo: d.info()}
	jobsMu.Unlock()
	out.Stdout = readTail(d.local(".out"), maxBytes)
	out.Stderr = readTail(d.local(".err"), maxBytes)
	return out, nil
}

func readTail(path string, max int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	prefix := ""
	if len(b) > max {
		// keep a UTF-16 BOM decodable: only UTF-8 output is truncated mid-stream
		if !bytes.HasPrefix(b, []byte{0xFF, 0xFE}) {
			b = b[len(b)-max:]
			prefix = "...[earlier output truncated]\n"
		}
	}
	s := strings.ReplaceAll(decodeOutput(b), "\r\n", "\n")
	return prefix + strings.TrimRight(s, "\n")
}

// JobCancel kills the job's process tree. Call inside Session.Do.
func JobCancel(o *session.Ops, id string) (*JobInfo, error) {
	d, err := getJob(o.Session().Name(), id)
	if err != nil {
		return nil, err
	}
	jobsMu.Lock()
	state := d.state
	jobsMu.Unlock()
	if state != "running" {
		in, _ := JobStatus(d.Machine, id)
		return in, nil
	}
	pid := d.pid()
	if pid <= 0 {
		return nil, errors.New("the job has not reported its process id yet; try again in a moment")
	}
	jobsMu.Lock()
	d.cancelled = true
	jobsMu.Unlock()
	res, err := RunCommand(o, "taskkill.exe /PID "+strconv.Itoa(pid)+" /T /F", "cmd", 30*time.Second)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 && !strings.Contains(res.Stdout+res.Stderr, "not found") {
		return nil, fmt.Errorf("taskkill failed: %s", strings.TrimSpace(res.Stdout+" "+res.Stderr))
	}
	for i := 0; i < 20 && !refresh(d); i++ {
		_ = o.Sleep(250 * time.Millisecond)
	}
	return JobStatus(d.Machine, id)
}

// JobRemove forgets a finished job and deletes its files.
func JobRemove(machine, id string) error {
	d, err := getJob(machine, id)
	if err != nil {
		return err
	}
	jobsMu.Lock()
	defer jobsMu.Unlock()
	if d.state == "running" {
		return errors.New("job is still running; cancel it first")
	}
	d.cleanup()
	delete(jobs, id)
	return nil
}
