package config

import (
	"strings"
	"testing"
	"time"
)

func TestParseResolveValidate(t *testing.T) {
	t.Setenv("PW", "secret")
	c, err := Parse([]byte(`
server:
  listen: 127.0.0.1:9000
defaults:
  width: 1600
  height: 900
  idle_disconnect: 10m
machines:
  - name: a
    host: 10.0.0.1
    username: u
    password: ${PW}
  - name: b
    host: 10.0.0.2
    username: u
    password_cmd: echo hi
    width: 1024
    height: 768
    idle_disconnect: "0"
    security: nla
`), "x.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	a := c.Resolve(c.Machines[0])
	if a.Width != 1600 || a.Port != 3389 || a.IdleDisconnect != 10*time.Minute || !a.EnableDrive || a.Security != "auto" {
		t.Fatalf("bad defaults: %+v", a)
	}
	b := c.Resolve(c.Machines[1])
	if b.Width != 1024 || b.IdleDisconnect != 0 || b.Security != "nla" {
		t.Fatalf("bad overrides: %+v", b)
	}
	pw, err := ResolvePassword(t.Context(), c.Machines[0], nil)
	if err != nil || pw != "secret" {
		t.Fatalf("env password: %q %v", pw, err)
	}
	pw, err = ResolvePassword(t.Context(), c.Machines[1], nil)
	if err != nil || pw != "hi" {
		t.Fatalf("cmd password: %q %v", pw, err)
	}
}

func TestValidateErrors(t *testing.T) {
	c, err := Parse([]byte(`
server:
  listen: 0.0.0.0:8765
machines:
  - name: "bad name"
    host: ""
    username: u
  - name: x
    host: h
    username: u
    cert: fingerprint
`), "x.yaml")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Validate()
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"name must match", "host is required", "cert_fingerprint", "mcp_token is required", "admin_token is required"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
}

func TestUnknownFieldRejected(t *testing.T) {
	if _, err := Parse([]byte("machines:\n  - name: a\n    hots: x\n"), "x.yaml"); err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestEmpty(t *testing.T) {
	c, err := Parse(nil, "x.yaml")
	if err != nil || len(c.Machines) != 0 {
		t.Fatal(err)
	}
}
