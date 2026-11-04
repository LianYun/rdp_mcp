// Package worker implements the service side of the remoter-worker IPC protocol (docs/worker-ipc.md).
package worker

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

const ProtocolVersion = 1

// Frame is a raw BGRA image sent by the worker.
type Frame struct {
	Width, Height, Stride int
	ReqID                 uint32
	Pix                   []byte
}

// Event is an unsolicited message from the worker.
type Event struct {
	Name string
	Raw  json.RawMessage
}

func (e Event) Decode(v any) error { return json.Unmarshal(e.Raw, v) }

// Reply is a response to a request.
type Reply struct {
	Raw   json.RawMessage
	Frame *Frame
}

func (r Reply) Decode(v any) error { return json.Unmarshal(r.Raw, v) }

// RemoteError is returned when the worker answers ok=false.
type RemoteError struct {
	Op      string
	Message string
	Code    uint32
}

func (e *RemoteError) Error() string { return e.Message }

type Client struct {
	cmd    *exec.Cmd
	conn   net.Conn
	sock   string
	log    *slog.Logger
	wmu    sync.Mutex
	nextID atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan Reply
	frames  map[uint32]*Frame

	events chan Event
	done   chan struct{}
	// last "disconnected" event, recorded synchronously in the read loop
	discCode   uint32
	discReason string
	once       sync.Once
	err        error
	Hello      struct {
		Version  string `json:"version"`
		FreeRDP  string `json:"freerdp"`
		Protocol int    `json:"protocol"`
	}
}

type StartOptions struct {
	WorkerPath string
	SocketDir  string
	Name       string
	LogFile    string // worker stdout/stderr destination
	Env        []string
	Logger     *slog.Logger
}

var sockSeq atomic.Int64

// Start launches a worker process and waits for its hello event.
func Start(ctx context.Context, o StartOptions) (*Client, error) {
	if err := os.MkdirAll(o.SocketDir, 0o700); err != nil {
		return nil, err
	}
	sock := filepath.Join(o.SocketDir, fmt.Sprintf("w%d-%d.sock", os.Getpid(), sockSeq.Add(1)))
	if len(sock) > 100 {
		sock = filepath.Join(os.TempDir(), fmt.Sprintf("remoter-mcp-w%d-%d.sock", os.Getpid(), sockSeq.Add(1)))
	}
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return nil, fmt.Errorf("listen ipc socket: %w", err)
	}
	defer ln.Close()
	_ = os.Chmod(sock, 0o600)

	cmd := exec.Command(o.WorkerPath, "--ipc", sock)
	cmd.Env = append(os.Environ(), o.Env...)
	var logf *os.File
	if o.LogFile != "" {
		if f, err := os.OpenFile(o.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
			logf = f
			cmd.Stdout, cmd.Stderr = f, f
		}
	}
	if err := cmd.Start(); err != nil {
		if logf != nil {
			logf.Close()
		}
		_ = os.Remove(sock)
		return nil, fmt.Errorf("start worker %s: %w", o.WorkerPath, err)
	}

	c := &Client{
		cmd:     cmd,
		sock:    sock,
		log:     o.Logger,
		pending: map[int64]chan Reply{},
		frames:  map[uint32]*Frame{},
		events:  make(chan Event, 256),
		done:    make(chan struct{}),
	}
	if c.log == nil {
		c.log = slog.Default()
	}
	exited := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		if logf != nil {
			logf.Close()
		}
		exited <- err
		c.fail(fmt.Errorf("worker exited: %v", err))
	}()

	type acc struct {
		conn net.Conn
		err  error
	}
	ach := make(chan acc, 1)
	go func() {
		conn, err := ln.Accept()
		ach <- acc{conn, err}
	}()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case a := <-ach:
		if a.err != nil {
			c.Kill()
			return nil, a.err
		}
		c.conn = a.conn
	case err := <-exited:
		_ = os.Remove(sock)
		return nil, fmt.Errorf("worker exited before connecting: %v (see %s)", err, o.LogFile)
	case <-timer.C:
		c.Kill()
		return nil, errors.New("timed out waiting for worker to connect")
	case <-ctx.Done():
		c.Kill()
		return nil, ctx.Err()
	}
	_ = os.Remove(sock)

	go c.readLoop()

	select {
	case ev := <-c.events:
		if ev.Name != "hello" {
			c.Kill()
			return nil, fmt.Errorf("unexpected first event %q", ev.Name)
		}
		_ = ev.Decode(&c.Hello)
		if c.Hello.Protocol != ProtocolVersion {
			c.Kill()
			return nil, fmt.Errorf("worker protocol %d, want %d", c.Hello.Protocol, ProtocolVersion)
		}
	case <-c.done:
		return nil, c.Err()
	case <-time.After(10 * time.Second):
		c.Kill()
		return nil, errors.New("timed out waiting for worker hello")
	}
	return c, nil
}

func (c *Client) Events() <-chan Event  { return c.events }
func (c *Client) Done() <-chan struct{} { return c.done }

func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *Client) fail(err error) {
	c.once.Do(func() {
		c.mu.Lock()
		c.err = err
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		c.mu.Unlock()
		if c.conn != nil {
			_ = c.conn.Close()
		}
		close(c.done)
	})
}

// Kill terminates the worker immediately.
func (c *Client) Kill() {
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	c.fail(errors.New("worker killed"))
}

// Close asks the worker to disconnect and waits briefly before killing it.
func (c *Client) Close() {
	select {
	case <-c.done:
		return
	default:
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = c.Call(ctx, "disconnect", nil)
	select {
	case <-c.done:
	case <-time.After(3 * time.Second):
		c.Kill()
	}
}

func (c *Client) readLoop() {
	r := bufio.NewReaderSize(c.conn, 1<<20)
	var hdr [4]byte
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			c.fail(fmt.Errorf("ipc read: %w", err))
			return
		}
		n := binary.BigEndian.Uint32(hdr[:])
		if n == 0 || n > 256<<20 {
			c.fail(fmt.Errorf("ipc: bad frame length %d", n))
			return
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			c.fail(fmt.Errorf("ipc read: %w", err))
			return
		}
		switch buf[0] {
		case 'F':
			p := buf[1:]
			if len(p) < 16 {
				continue
			}
			f := &Frame{
				Width:  int(binary.BigEndian.Uint32(p[0:])),
				Height: int(binary.BigEndian.Uint32(p[4:])),
				Stride: int(binary.BigEndian.Uint32(p[8:])),
				ReqID:  binary.BigEndian.Uint32(p[12:]),
				Pix:    p[16:],
			}
			c.mu.Lock()
			c.frames[f.ReqID] = f
			c.mu.Unlock()
		case 'J':
			c.handleJSON(buf[1:])
		}
	}
}

func (c *Client) handleJSON(b []byte) {
	var head struct {
		ID    *int64 `json:"id"`
		Event string `json:"event"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		c.log.Warn("ipc: bad json from worker", "err", err)
		return
	}
	if head.Event == "disconnected" {
		var d struct {
			Code   uint32 `json:"code"`
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(b, &d)
		c.mu.Lock()
		c.discCode, c.discReason = d.Code, d.Reason
		c.mu.Unlock()
	}
	if head.Event != "" {
		select {
		case c.events <- Event{Name: head.Event, Raw: b}:
		default:
			c.log.Warn("ipc: dropping worker event", "event", head.Event)
		}
		return
	}
	if head.ID == nil {
		return
	}
	c.mu.Lock()
	ch := c.pending[*head.ID]
	delete(c.pending, *head.ID)
	f := c.frames[uint32(*head.ID)]
	delete(c.frames, uint32(*head.ID))
	c.mu.Unlock()
	if ch != nil {
		ch <- Reply{Raw: b, Frame: f}
	}
}

// Call sends a request and waits for its reply.
func (c *Client) Call(ctx context.Context, op string, params map[string]any) (Reply, error) {
	id := c.nextID.Add(1)
	msg := map[string]any{}
	for k, v := range params {
		msg[k] = v
	}
	msg["id"] = id
	msg["op"] = op
	b, err := json.Marshal(msg)
	if err != nil {
		return Reply{}, err
	}
	ch := make(chan Reply, 1)
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return Reply{}, c.err
	}
	c.pending[id] = ch
	c.mu.Unlock()

	frame := make([]byte, 5+len(b))
	binary.BigEndian.PutUint32(frame, uint32(len(b)+1))
	frame[4] = 'J'
	copy(frame[5:], b)
	c.wmu.Lock()
	_, err = c.conn.Write(frame)
	c.wmu.Unlock()
	if err != nil {
		c.fail(fmt.Errorf("ipc write: %w", err))
		return Reply{}, c.Err()
	}

	select {
	case r, ok := <-ch:
		if !ok {
			return Reply{}, c.Err()
		}
		var st struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
			Code  uint32 `json:"code"`
		}
		if err := json.Unmarshal(r.Raw, &st); err != nil {
			return Reply{}, err
		}
		if !st.OK {
			return r, &RemoteError{Op: op, Message: st.Error, Code: st.Code}
		}
		return r, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return Reply{}, ctx.Err()
	}
}

// Disconnect returns the FreeRDP error code and reason of the last "disconnected" event.
// It is complete once Done() is closed.
func (c *Client) Disconnect() (uint32, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.discCode, c.discReason
}
