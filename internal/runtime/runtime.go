// Package runtime handles the single-instance lock and the service.json runtime file.
package runtime

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Info is written to <data_dir>/run/service.json (mode 0600) so UI/CLI can find the service.
type Info struct {
	PID        int       `json:"pid"`
	Listen     string    `json:"listen"`
	AdminToken string    `json:"admin_token"`
	Version    string    `json:"version"`
	StartedAt  time.Time `json:"started_at"`
	ConfigPath string    `json:"config_path"`
	ManagedBy  string    `json:"managed_by"` // cli | ui-sidecar | launchd | systemd
}

type Runtime struct {
	dir  string
	lock *os.File
}

func RunDir(dataDir string) string { return filepath.Join(dataDir, "run") }

// ErrAlreadyRunning is returned by Acquire when another instance holds the lock.
var ErrAlreadyRunning = errors.New("another remoter-mcp instance is already running")

// Acquire takes the single-instance lock.
func Acquire(dataDir string) (*Runtime, error) {
	dir := RunDir(dataDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "remoter-mcp.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, ErrAlreadyRunning
	}
	return &Runtime{dir: dir, lock: f}, nil
}

func (r *Runtime) WriteInfo(info Info) error {
	b, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(r.dir, "service.json.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(r.dir, "service.json"))
}

func (r *Runtime) Release() {
	_ = os.Remove(filepath.Join(r.dir, "service.json"))
	if r.lock != nil {
		_ = syscall.Flock(int(r.lock.Fd()), syscall.LOCK_UN)
		_ = r.lock.Close()
	}
}

// ReadInfo reads service.json; ok is false if missing or the process is gone.
func ReadInfo(dataDir string) (Info, bool, error) {
	var info Info
	b, err := os.ReadFile(filepath.Join(RunDir(dataDir), "service.json"))
	if errors.Is(err, os.ErrNotExist) {
		return info, false, nil
	}
	if err != nil {
		return info, false, err
	}
	if err := json.Unmarshal(b, &info); err != nil {
		return info, false, fmt.Errorf("parse service.json: %w", err)
	}
	if info.PID <= 0 || syscall.Kill(info.PID, 0) != nil {
		return info, false, nil
	}
	return info, true, nil
}

// NewToken returns a random hex token.
func NewToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
