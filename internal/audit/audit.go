// Package audit records MCP tool calls as JSONL and publishes them on the event bus.
package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LianYun/rdp_mcp/internal/events"
)

type Record struct {
	ID         string          `json:"id"`
	Time       time.Time       `json:"time"`
	Phase      string          `json:"phase"` // start | end
	Tool       string          `json:"tool"`
	Machine    string          `json:"machine,omitempty"`
	Args       json.RawMessage `json:"args,omitempty"`
	DurationMs int64           `json:"duration_ms,omitempty"`
	OK         *bool           `json:"ok,omitempty"`
	Summary    string          `json:"summary,omitempty"`
}

type Logger struct {
	dir        string
	enabled    bool
	redactType bool
	retention  int
	bus        *events.Bus
	seq        atomic.Int64
	mu         sync.Mutex
}

func New(dir string, enabled, redactTypedText bool, retentionDays int, bus *events.Bus) *Logger {
	l := &Logger{dir: dir, enabled: enabled, redactType: redactTypedText, retention: retentionDays, bus: bus}
	if enabled {
		_ = os.MkdirAll(dir, 0o700)
		l.prune()
	}
	return l
}

func (l *Logger) args(in any) json.RawMessage {
	b, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	if l.redactType {
		var m map[string]any
		if json.Unmarshal(b, &m) == nil {
			for _, k := range []string{"text", "content", "command"} {
				if _, ok := m[k]; ok {
					m[k] = "[redacted]"
				}
			}
			b, _ = json.Marshal(m)
		}
	}
	return b
}

// Begin records the start of a tool call and returns its id.
func (l *Logger) Begin(tool, machine string, in any) string {
	id := fmt.Sprintf("tc_%d_%d", time.Now().UnixMilli(), l.seq.Add(1))
	rec := Record{ID: id, Time: time.Now(), Phase: "start", Tool: tool, Machine: machine, Args: l.args(in)}
	if l.bus != nil {
		l.bus.Publish("tool_call", rec)
	}
	return id
}

// End records the end of a tool call.
func (l *Logger) End(id, tool, machine string, in any, d time.Duration, isError bool, summary string) {
	ok := !isError
	rec := Record{ID: id, Time: time.Now(), Phase: "end", Tool: tool, Machine: machine, Args: l.args(in),
		DurationMs: d.Milliseconds(), OK: &ok, Summary: summary}
	if l.bus != nil {
		l.bus.Publish("tool_call", rec)
	}
	if !l.enabled {
		return
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(filepath.Join(l.dir, rec.Time.Format("2006-01-02")+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	_, _ = f.Write(append(b, '\n'))
	_ = f.Close()
}

// Query returns recent end records, newest first.
func (l *Logger) Query(machine, tool string, limit int) ([]Record, error) {
	if limit <= 0 {
		limit = 100
	}
	files, _ := filepath.Glob(filepath.Join(l.dir, "*.jsonl"))
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	var out []Record
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			var r Record
			if json.Unmarshal([]byte(lines[i]), &r) != nil {
				continue
			}
			if (machine != "" && r.Machine != machine) || (tool != "" && r.Tool != tool) {
				continue
			}
			out = append(out, r)
			if len(out) >= limit {
				return out, nil
			}
		}
	}
	return out, nil
}

func (l *Logger) prune() {
	if l.retention <= 0 {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -l.retention).Format("2006-01-02")
	files, _ := filepath.Glob(filepath.Join(l.dir, "*.jsonl"))
	for _, f := range files {
		if strings.TrimSuffix(filepath.Base(f), ".jsonl") < cutoff {
			_ = os.Remove(f)
		}
	}
}
