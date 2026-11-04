// Package session manages one RDP session (worker process) per configured machine.
package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/LianYun/rdp_mcp/internal/config"
	"github.com/LianYun/rdp_mcp/internal/events"
)

type State string

const (
	StateIdle         State = "idle"
	StateConnecting   State = "connecting"
	StateConnected    State = "connected"
	StateReconnecting State = "reconnecting"
	StateFailed       State = "failed"
)

type ControlMode string

const (
	ModeAgent  ControlMode = "agent"
	ModeHuman  ControlMode = "human"
	ModePaused ControlMode = "paused"
)

// Source identifies who issues an operation.
type Source int

const (
	SourceAgent Source = iota // MCP tool call
	SourceHuman               // UI live view takeover input
	SourceAdmin               // UI/CLI management action
)

var (
	ErrUnknownMachine = errors.New("unknown machine")
	ErrDisabled       = errors.New("machine is disabled")
)

// ControlError is returned when the control mode forbids the operation.
type ControlError struct {
	Machine string
	Mode    ControlMode
}

func (e *ControlError) Error() string {
	if e.Mode == ModeHuman {
		return fmt.Sprintf("machine %q is under human control (taken over in the UI); wait until it is handed back", e.Machine)
	}
	return fmt.Sprintf("machine %q is paused by the operator; agent actions are not allowed right now", e.Machine)
}

type Options struct {
	WorkerPath  string
	DataDir     string
	Logger      *slog.Logger
	Bus         *events.Bus
	KeychainGet func(key string) (string, error)
	WorkerDebug bool
}

type Manager struct {
	opts  Options
	known *KnownHosts

	mu       sync.RWMutex
	cfg      *config.Config
	sessions map[string]*Session

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewManager(cfg *config.Config, opts Options) *Manager {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Bus == nil {
		opts.Bus = events.NewBus()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		opts:     opts,
		known:    NewKnownHosts(filepath.Join(opts.DataDir, "known_hosts")),
		sessions: map[string]*Session{},
		ctx:      ctx,
		cancel:   cancel,
	}
	m.UpdateConfig(cfg)
	return m
}

func (m *Manager) KnownHosts() *KnownHosts { return m.known }
func (m *Manager) Bus() *events.Bus        { return m.opts.Bus }

func (m *Manager) Config() *config.Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

// UpdateConfig applies a new config: adds, updates and removes sessions.
func (m *Manager) UpdateConfig(cfg *config.Config) {
	m.mu.Lock()
	m.cfg = cfg
	seen := map[string]bool{}
	var removed []*Session
	for _, mc := range cfg.Machines {
		seen[mc.Name] = true
		r := cfg.Resolve(mc)
		if s, ok := m.sessions[mc.Name]; ok {
			s.mu.Lock()
			s.cfg = r
			s.mu.Unlock()
			continue
		}
		m.sessions[mc.Name] = &Session{m: m, name: mc.Name, cfg: r, state: StateIdle, mode: ModeAgent}
	}
	for name, s := range m.sessions {
		if !seen[name] {
			removed = append(removed, s)
			delete(m.sessions, name)
		}
	}
	m.mu.Unlock()
	for _, s := range removed {
		s.Disconnect()
	}
}

// Get returns the session for name.
func (m *Manager) Get(name string) (*Session, error) {
	m.mu.RLock()
	s, ok := m.sessions[name]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w %q (use list_machines to see available machines)", ErrUnknownMachine, name)
	}
	if !s.Resolved().IsEnabled {
		return nil, fmt.Errorf("%w: %q", ErrDisabled, name)
	}
	return s, nil
}

// Lookup returns the session for name regardless of its enabled flag (management API).
func (m *Manager) Lookup(name string) (*Session, error) {
	m.mu.RLock()
	s, ok := m.sessions[name]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrUnknownMachine, name)
	}
	return s, nil
}

// List returns all sessions in config order.
func (m *Manager) List() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	order := map[string]int{}
	for i, mc := range m.cfg.Machines {
		order[mc.Name] = i
	}
	out := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return order[out[i].name] < order[out[j].name] })
	return out
}

// Start connects auto_connect machines and runs the idle reaper.
func (m *Manager) Start() {
	for _, s := range m.List() {
		r := s.Resolved()
		if r.IsEnabled && r.AutoConnect {
			s := s
			go func() {
				ctx, cancel := context.WithTimeout(m.ctx, 90*time.Second)
				defer cancel()
				if err := s.Connect(ctx); err != nil {
					m.opts.Logger.Warn("auto connect failed", "machine", s.name, "err", err)
				}
			}()
		}
	}
	m.wg.Add(1)
	go m.idleLoop()
}

func (m *Manager) idleLoop() {
	defer m.wg.Done()
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-t.C:
			for _, s := range m.List() {
				s.reapIfIdle()
			}
		}
	}
}

// EmergencyStop pauses all machines.
func (m *Manager) EmergencyStop() {
	for _, s := range m.List() {
		s.SetMode(ModePaused)
	}
}

// Shutdown disconnects all sessions.
func (m *Manager) Shutdown() {
	m.cancel()
	var wg sync.WaitGroup
	for _, s := range m.List() {
		wg.Add(1)
		go func(s *Session) { defer wg.Done(); s.Disconnect() }(s)
	}
	wg.Wait()
	m.wg.Wait()
}

// FindWorker locates the rdp-worker binary.
func FindWorker(configured string) (string, error) {
	if configured != "" {
		p := config.ExpandHome(configured)
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("worker_path %s: %w", p, err)
		}
		return p, nil
	}
	if exe, err := os.Executable(); err == nil {
		exe, _ = filepath.EvalSymlinks(exe)
		dir := filepath.Dir(exe)
		for _, c := range []string{
			filepath.Join(dir, "rdp-worker"),
			filepath.Join(dir, "..", "worker", "build", "rdp-worker"),
			filepath.Join(dir, "..", "..", "worker", "build", "rdp-worker"),
		} {
			if st, err := os.Stat(c); err == nil && !st.IsDir() {
				return filepath.Clean(c), nil
			}
		}
	}
	if p, err := exec.LookPath("rdp-worker"); err == nil {
		return p, nil
	}
	return "", errors.New("rdp-worker not found: build it (make worker) or set server.worker_path")
}
