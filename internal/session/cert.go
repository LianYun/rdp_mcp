package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/LianYun/rdp_mcp/internal/worker"
)

// certInfo mirrors the worker's cert_verify event.
type certInfo struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	CommonName  string `json:"common_name"`
	Subject     string `json:"subject"`
	Issuer      string `json:"issuer"`
	Fingerprint string `json:"fingerprint"`
}

// certPrompt is a certificate verification waiting for the operator's answer.
type certPrompt struct {
	info     certInfo
	reason   string
	decision chan string // buffered 1; "once" | "always" | "reject"
}

// certPromptTimeout bounds how long a connect waits for the UI to answer.
const certPromptTimeout = 2 * time.Minute

// CertPrompt is the exported view of a pending certificate verification.
type CertPrompt struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Fingerprint string `json:"fingerprint"`
	Subject     string `json:"subject"`
	Issuer      string `json:"issuer"`
	Reason      string `json:"reason"`
}

// PendingCert returns the unanswered certificate verification, if any.
func (s *Session) PendingCert() (CertPrompt, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pendingCert == nil {
		return CertPrompt{}, false
	}
	p := s.pendingCert
	return CertPrompt{
		Host: p.info.Host, Port: p.info.Port, Fingerprint: p.info.Fingerprint,
		Subject: p.info.Subject, Issuer: p.info.Issuer, Reason: p.reason,
	}, true
}

// AnswerCert delivers the operator's decision to a pending certificate prompt.
func (s *Session) AnswerCert(decision string) error {
	switch decision {
	case "once", "always", "reject":
	default:
		return fmt.Errorf("accept must be once|always|reject")
	}
	s.mu.Lock()
	p := s.pendingCert
	s.mu.Unlock()
	if p == nil {
		return errors.New("no certificate verification is waiting for this machine")
	}
	select {
	case p.decision <- decision:
		return nil
	default:
		return errors.New("the certificate verification was already answered")
	}
}

// handleCertVerify decides a certificate: automatically when policy/known_hosts
// allow, otherwise by asking the operator through the UI (with a timeout that
// rejects). The worker blocks until cert_reply, so a reply must always be sent
// while the connection is alive.
func (s *Session) handleCertVerify(w *worker.Client, c certInfo, log *slog.Logger) {
	accept, reason := s.decideCert(c.Host, c.Port, c.Fingerprint)
	if accept {
		replyCert(w, 1)
		return
	}
	if s.quiet { // test connections never prompt
		s.mu.Lock()
		s.lastErr = reason
		s.mu.Unlock()
		replyCert(w, 0)
		return
	}
	p := &certPrompt{info: c, reason: reason, decision: make(chan string, 1)}
	s.mu.Lock()
	s.pendingCert = p
	s.lastErr = reason
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.pendingCert == p {
			s.pendingCert = nil
		}
		s.mu.Unlock()
	}()
	s.m.opts.Bus.Publish("cert_verify", map[string]any{
		"machine": s.name, "host": c.Host, "port": c.Port, "fingerprint": c.Fingerprint,
		"subject": c.Subject, "issuer": c.Issuer, "reason": reason, "pending": true,
	})
	log.Warn("waiting for certificate decision", "machine", s.name, "fingerprint", c.Fingerprint)

	decision := "reject"
	select {
	case decision = <-p.decision:
	case <-time.After(certPromptTimeout):
		log.Warn("certificate prompt timed out; rejecting", "machine", s.name)
	case <-w.Done():
		return // worker gone; nobody left to reply to
	}
	switch decision {
	case "once":
		log.Info("certificate accepted for this connection", "machine", s.name)
		replyCert(w, 1)
	case "always":
		key := c.Host + ":" + strconv.Itoa(c.Port)
		if err := s.m.known.Set(key, c.Fingerprint); err != nil {
			log.Error("save known_hosts", "err", err)
		} else {
			log.Info("certificate trusted permanently", "machine", s.name, "host", key, "fingerprint", c.Fingerprint)
		}
		replyCert(w, 1)
	default:
		log.Warn("certificate rejected", "machine", s.name)
		replyCert(w, 0)
	}
}

func replyCert(w *worker.Client, accept int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = w.Call(ctx, "cert_reply", map[string]any{"accept": accept})
}
