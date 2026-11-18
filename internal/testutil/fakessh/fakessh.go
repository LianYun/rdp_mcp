// Package fakessh is an SSH server for tests: it runs "exec" requests with /bin/sh -c
// on the local machine, with HOME set to a test directory.
package fakessh

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"io"
	"net"
	"os"
	"os/exec"
	"syscall"
	"testing"

	"golang.org/x/crypto/ssh"
)

// Start serves on 127.0.0.1 and returns the port.
func Start(t testing.TB, user, pass, home string) int {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
			if c.User() == user && string(p) == pass {
				return nil, nil
			}
			return nil, io.ErrUnexpectedEOF
		},
	}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serveConn(c, cfg, home)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func serveConn(c net.Conn, cfg *ssh.ServerConfig, home string) {
	_, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		c.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			nc.Reject(ssh.UnknownChannelType, "only sessions")
			continue
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go serveSession(ch, creqs, home)
	}
}

func serveSession(ch ssh.Channel, reqs <-chan *ssh.Request, home string) {
	defer ch.Close()
	var cmd *exec.Cmd
	done := make(chan struct{})
	for req := range reqs {
		switch req.Type {
		case "exec":
			if cmd != nil || len(req.Payload) < 4 {
				req.Reply(false, nil)
				continue
			}
			line := string(req.Payload[4:])
			cmd = exec.Command("/bin/sh", "-c", line)
			cmd.Env = append(os.Environ(), "HOME="+home)
			cmd.Dir = home
			cmd.Stdin, cmd.Stdout, cmd.Stderr = ch, ch, ch.Stderr()
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			if err := cmd.Start(); err != nil {
				req.Reply(false, nil)
				return
			}
			req.Reply(true, nil)
			go func() {
				code := 0
				if err := cmd.Wait(); err != nil {
					if ee, ok := err.(*exec.ExitError); ok {
						code = ee.ExitCode()
					} else {
						code = 255
					}
				}
				status := make([]byte, 4)
				binary.BigEndian.PutUint32(status, uint32(code))
				ch.SendRequest("exit-status", false, status)
				ch.Close()
				close(done)
			}()
		case "signal":
			if cmd != nil && cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
	if cmd != nil {
		<-done
	}
}
