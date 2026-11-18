package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/LianYun/rdp_mcp/internal/config"
)

// SSH for macOS machines (docs/design.md §5a): commands, files, clipboard and paste-typing.
// One client per session, dialed on demand, closed on Disconnect or when idle.

// SSHKnownHostsKey is the known_hosts key of a Mac's SSH host key.
func SSHKnownHostsKey(host string, port int) string {
	return "ssh:" + host + ":" + strconv.Itoa(port)
}

// ErrNoSSH is returned when SSH is not available for the machine.
var ErrNoSSH = errors.New("SSH is disabled for this machine (enable_ssh: false); run_command, file and clipboard need it")

func (s *Session) sshCheck() (config.Resolved, error) {
	r := s.Resolved()
	if !r.IsMac() {
		return r, errors.New("SSH is only used for macOS (protocol: vnc) machines")
	}
	if !r.SSHEnabled {
		return r, ErrNoSSH
	}
	return r, nil
}

// SSHAvailable reports whether the machine is a Mac with SSH enabled.
func (s *Session) SSHAvailable() bool {
	_, err := s.sshCheck()
	return err == nil
}

// SSH returns a connected client, dialing if needed.
func (s *Session) SSH(ctx context.Context) (*ssh.Client, error) {
	r, err := s.sshCheck()
	if err != nil {
		return nil, err
	}
	s.sshMu.Lock()
	defer s.sshMu.Unlock()
	s.sshUsed = time.Now()
	if s.sshc != nil {
		return s.sshc, nil
	}
	pass, err := config.ResolvePassword(ctx, r.Machine, s.m.opts.KeychainGet)
	if err != nil {
		return nil, err
	}
	addr := net.JoinHostPort(r.Host, strconv.Itoa(r.SSHPort))
	cfg := &ssh.ClientConfig{
		User: r.Username,
		Auth: []ssh.AuthMethod{
			ssh.Password(pass),
			ssh.KeyboardInteractive(func(_, _ string, qs []string, _ []bool) ([]string, error) {
				a := make([]string, len(qs))
				for i := range a {
					a[i] = pass
				}
				return a, nil
			}),
		},
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			return s.checkSSHHostKey(r, key)
		},
		Timeout: 15 * time.Second,
	}
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(dctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("SSH connect %s: %w (is Remote Login enabled on the Mac?)", addr, err)
	}
	if dl, ok := dctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	cc, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		if strings.Contains(err.Error(), "unable to authenticate") {
			return nil, fmt.Errorf("SSH login to %s as %s failed: check the password and that the account may use Remote Login", addr, r.Username)
		}
		return nil, fmt.Errorf("SSH %s: %w", addr, err)
	}
	_ = conn.SetDeadline(time.Time{})
	c := ssh.NewClient(cc, chans, reqs)
	s.sshc = c
	s.m.opts.Logger.Info("ssh connected", "machine", s.name, "addr", addr)
	go s.sshKeepalive(c)
	return c, nil
}

func (s *Session) sshKeepalive(c *ssh.Client) {
	done := make(chan struct{})
	go func() { _ = c.Wait(); close(done) }()
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-done:
			s.dropSSH(c)
			return
		case <-t.C:
			errc := make(chan error, 1)
			go func() { _, _, err := c.SendRequest("keepalive@openssh.com", true, nil); errc <- err }()
			select {
			case err := <-errc:
				if err != nil {
					c.Close()
				}
			case <-time.After(15 * time.Second):
				c.Close()
			}
		}
	}
}

func (s *Session) dropSSH(c *ssh.Client) {
	s.sshMu.Lock()
	if s.sshc == c {
		s.sshc = nil
	}
	s.sshMu.Unlock()
}

// closeSSH closes the SSH client, if any.
func (s *Session) closeSSH() {
	s.sshMu.Lock()
	c := s.sshc
	s.sshc = nil
	s.sshMu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

func (s *Session) reapSSHIfIdle(idle time.Duration) {
	if idle <= 0 {
		return
	}
	s.sshMu.Lock()
	c := s.sshc
	stale := c != nil && time.Since(s.sshUsed) > idle
	if stale {
		s.sshc = nil
	}
	s.sshMu.Unlock()
	if stale {
		s.m.opts.Logger.Info("closing idle ssh connection", "machine", s.name)
		_ = c.Close()
	}
}

func (s *Session) checkSSHHostKey(r config.Resolved, key ssh.PublicKey) error {
	fp := ssh.FingerprintSHA256(key)
	k := SSHKnownHostsKey(r.Host, r.SSHPort)
	switch r.Cert {
	case "ignore":
		return nil
	case "fingerprint":
		if NormalizeFingerprint(fp) == NormalizeFingerprint(r.CertFingerprint) {
			return nil
		}
		return fmt.Errorf("SSH host key fingerprint mismatch for %s: got %s", k, fp)
	}
	known, ok := s.m.known.Get(k)
	if !ok {
		if err := s.m.known.Set(k, fp); err != nil {
			s.m.opts.Logger.Warn("save known_hosts", "err", err)
		}
		s.m.opts.Logger.Info("trusted new SSH host key (TOFU)", "machine", s.name, "host", k, "fingerprint", fp)
		return nil
	}
	if known == NormalizeFingerprint(fp) {
		return nil
	}
	return fmt.Errorf("SSH host key for %s changed (now %s); if expected, run `remoter-mcp trust %s` or remove the entry from %s",
		k, fp, s.name, filepath.Join(s.m.opts.DataDir, "known_hosts"))
}

// SSHResult is the outcome of a remote command.
type SSHResult struct {
	Stdout, Stderr []byte
	ExitCode       int
}

// SSHRun runs a shell command line on the Mac (via the account's login shell as sshd runs it),
// feeding stdin. A broken connection is redialed once.
func (s *Session) SSHRun(ctx context.Context, cmd string, stdin io.Reader) (*SSHResult, error) {
	for attempt := 0; ; attempt++ {
		c, err := s.SSH(ctx)
		if err != nil {
			return nil, err
		}
		sess, err := c.NewSession()
		if err != nil {
			s.dropSSH(c)
			_ = c.Close()
			if attempt == 0 && ctx.Err() == nil {
				continue
			}
			return nil, fmt.Errorf("SSH session: %w", err)
		}
		res, err := runSSHSession(ctx, sess, cmd, stdin, nil)
		if err != nil && attempt == 0 && stdin == nil && isConnErr(err) && ctx.Err() == nil {
			s.dropSSH(c)
			_ = c.Close()
			continue
		}
		return res, err
	}
}

func isConnErr(err error) bool {
	var ee *ssh.ExitError
	if errors.As(err, &ee) {
		return false
	}
	return errors.Is(err, io.EOF) || strings.Contains(err.Error(), "connection") || strings.Contains(err.Error(), "closed")
}

// SSHStream is like SSHRun but writes stdout to w instead of capturing it (no retry).
func (s *Session) SSHStream(ctx context.Context, cmd string, stdin io.Reader, w io.Writer) (*SSHResult, error) {
	c, err := s.SSH(ctx)
	if err != nil {
		return nil, err
	}
	sess, err := c.NewSession()
	if err != nil {
		s.dropSSH(c)
		_ = c.Close()
		return nil, fmt.Errorf("SSH session: %w", err)
	}
	return runSSHSession(ctx, sess, cmd, stdin, w)
}

func runSSHSession(ctx context.Context, sess *ssh.Session, cmd string, stdin io.Reader, stdout io.Writer) (*SSHResult, error) {
	defer sess.Close()
	var out, errb bytes.Buffer
	sess.Stdout, sess.Stderr = &out, &errb
	if stdout != nil {
		sess.Stdout = stdout
	}
	if stdin != nil {
		sess.Stdin = stdin
	}
	if err := sess.Start(cmd); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- sess.Wait() }()
	select {
	case err := <-done:
		res := &SSHResult{Stdout: out.Bytes(), Stderr: errb.Bytes()}
		var ee *ssh.ExitError
		var missing *ssh.ExitMissingError
		switch {
		case err == nil:
		case errors.As(err, &ee):
			res.ExitCode = ee.ExitStatus()
		case errors.As(err, &missing):
			res.ExitCode = -1
		default:
			return nil, err
		}
		return res, nil
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		_ = sess.Close()
		return nil, ctx.Err()
	}
}

// ShellQuote quotes s for a POSIX shell.
func ShellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

const utf8Env = "LANG=en_US.UTF-8 LC_ALL=en_US.UTF-8 "

// MacClipboardGet reads the Mac pasteboard text over SSH.
func (s *Session) MacClipboardGet(ctx context.Context) (string, error) {
	res, err := s.SSHRun(ctx, utf8Env+"pbpaste", nil)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("pbpaste failed: %s", strings.TrimSpace(string(res.Stderr)))
	}
	return string(res.Stdout), nil
}

// MacClipboardSet writes the Mac pasteboard text over SSH.
func (s *Session) MacClipboardSet(ctx context.Context, text string) error {
	res, err := s.SSHRun(ctx, utf8Env+"pbcopy", strings.NewReader(text))
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("pbcopy failed: %s", strings.TrimSpace(string(res.Stderr)))
	}
	return nil
}

// macPasteState reports whether paste-typing can be used (the SSH user owns the unlocked
// console session) and returns the current clipboard text to restore afterwards.
func (s *Session) macPasteState(ctx context.Context) (ok bool, clip string, err error) {
	const script = `if ioreg -n Root -d1 -a 2>/dev/null | grep -A1 CGSSessionScreenIsLocked | grep -q '<true/>'; then echo LOCKED; exit 0; fi
if [ "$(stat -f %Su /dev/console)" != "$(id -un)" ]; then echo NOTCONSOLE; exit 0; fi
echo OK; ` + utf8Env + `pbpaste`
	res, err := s.SSHRun(ctx, script, nil)
	if err != nil {
		return false, "", err
	}
	state, rest, _ := strings.Cut(string(res.Stdout), "\n")
	return state == "OK", rest, nil
}
