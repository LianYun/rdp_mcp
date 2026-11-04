package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const writerFixture = `# 顶部的说明注释
server:
  listen: 127.0.0.1:8765        # MCP: /mcp   管理 API: /api/v1
  screenshot_max_width: 1280

defaults:
  port: 3389
  security: auto             # nla | tls | rdp | auto

machines:
  # 开发机
  - name: dev-pc
    description: "开发机"     # 描述注释
    host: 192.168.1.20
    username: alice
    password: keychain:dev-pc
    tags: [dev]
    width: 2560

  - name: test-vm
    host: 192.168.1.30
    username: tester
    password: ${TEST_VM_PASSWORD}
    idle_disconnect: 30m
`

func writeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "configs.yaml")
	if err := os.WriteFile(p, []byte(writerFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAddMachinePreservesComments(t *testing.T) {
	p := writeFixture(t)
	_, err := AddMachine(p, Machine{
		Name: "win2", Description: "new", Host: "10.0.0.5", Username: "bob",
		Password: "keychain:win2", Tags: []string{"a", "b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	for _, want := range []string{"# 顶部的说明注释", "# 开发机", "# 描述注释", "# nla | tls | rdp | auto", "name: win2", "keychain:win2"} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q:\n%s", want, s)
		}
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Machine("win2"); !ok {
		t.Fatal("win2 not in reloaded config")
	}
	if _, ok := cfg.Machine("dev-pc"); !ok {
		t.Fatal("dev-pc lost")
	}
	m, _ := cfg.Machine("win2")
	if len(m.Tags) != 2 || m.Tags[0] != "a" {
		t.Errorf("tags = %v", m.Tags)
	}
}

func TestAddMachineDuplicate(t *testing.T) {
	p := writeFixture(t)
	if _, err := AddMachine(p, Machine{Name: "dev-pc", Host: "h", Username: "u"}); err == nil {
		t.Fatal("expected duplicate error")
	}
}

func TestAddMachineCreatesFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "configs.yaml")
	_, err := AddMachine(p, Machine{Name: "solo", Host: "h", Username: "u", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Machines) != 1 || cfg.Machines[0].Name != "solo" {
		t.Fatalf("machines = %+v", cfg.Machines)
	}
}

func TestUpdateMachineMerge(t *testing.T) {
	p := writeFixture(t)
	newName := "dev-pc2"
	_, err := UpdateMachine(p, "dev-pc", Machine{
		Name: newName, Description: "开发机v2", Host: "192.168.1.21", Username: "alice",
		Password: "keychain:dev-pc2", Tags: []string{"dev"},
		MachineOptions: MachineOptions{Width: 1920, IdleDisconnect: &Duration{Duration: time.Hour}},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	for _, want := range []string{"# 开发机", "# 描述注释", "name: dev-pc2", "开发机v2", "idle_disconnect: 1h"} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "2560") {
		t.Errorf("old width kept:\n%s", s)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := cfg.Machine(newName)
	if !ok {
		t.Fatal("renamed machine missing")
	}
	if m.Width != 1920 || m.IdleDisconnect.Duration != time.Hour {
		t.Errorf("machine = %+v", m)
	}
	if _, ok := cfg.Machine("dev-pc"); ok {
		t.Error("old name still present")
	}
}

func TestUpdateMachineRemovesOptionalFields(t *testing.T) {
	p := writeFixture(t)
	_, err := UpdateMachine(p, "test-vm", Machine{Name: "test-vm", Host: "192.168.1.30", Username: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if strings.Contains(string(b), "idle_disconnect") {
		t.Errorf("idle_disconnect not removed:\n%s", b)
	}
}

func TestUpdateMachineMissing(t *testing.T) {
	p := writeFixture(t)
	if _, err := UpdateMachine(p, "nope", Machine{Name: "nope", Host: "h", Username: "u"}); !os.IsNotExist(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestDeleteMachine(t *testing.T) {
	p := writeFixture(t)
	_, err := DeleteMachine(p, "dev-pc")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	if strings.Contains(s, "dev-pc") {
		t.Errorf("dev-pc still present:\n%s", s)
	}
	if !strings.Contains(s, "# 顶部的说明注释") || !strings.Contains(s, "test-vm") {
		t.Errorf("other content lost:\n%s", s)
	}
	if _, err := DeleteMachine(p, "dev-pc"); !os.IsNotExist(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestUpdateMachineFields(t *testing.T) {
	p := writeFixture(t)
	_, err := UpdateMachineFields(p, "dev-pc", map[string]any{
		"password":     "keychain:dev-pc",
		"password_cmd": nil,
		"port":         3390,
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := cfg.Machine("dev-pc")
	if m.Password != "keychain:dev-pc" || m.PasswordCmd != "" || m.Port != 3390 {
		t.Errorf("machine = %+v", m)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "# 描述注释") {
		t.Errorf("comment lost:\n%s", b)
	}
}

func TestSetMCPToken(t *testing.T) {
	p := writeFixture(t)
	_, err := SetMCPToken(p, "tok-123")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.MCPToken != "tok-123" {
		t.Errorf("token = %q", cfg.Server.MCPToken)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "# MCP: /mcp   管理 API: /api/v1") {
		t.Errorf("server comment lost:\n%s", b)
	}

	p2 := filepath.Join(t.TempDir(), "configs.yaml")
	if _, err := AddMachine(p2, Machine{Name: "x", Host: "h", Username: "u"}); err != nil {
		t.Fatal(err)
	}
	if _, err := SetMCPToken(p2, "t"); err != nil {
		t.Fatal(err)
	}
	cfg2, err := Load(p2)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Server.MCPToken != "t" || len(cfg2.Machines) != 1 {
		t.Errorf("cfg = %+v", cfg2)
	}
}

func TestSaveRawRejectsInvalid(t *testing.T) {
	p := writeFixture(t)
	bad := "machines:\n  - name: x\n    host: h\n    username: u\n    security: bogus\n"
	if _, err := SaveRaw(p, []byte(bad)); err == nil {
		t.Fatal("expected validation error")
	}
	b, _ := os.ReadFile(p)
	if strings.Contains(string(b), "bogus") {
		t.Error("file changed despite validation error")
	}
	good := "server:\n  listen: 127.0.0.1:9000\nmachines:\n  - name: y\n    host: h2\n    username: u2\n"
	if _, err := SaveRaw(p, []byte(good)); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Listen != "127.0.0.1:9000" || len(cfg.Machines) != 1 {
		t.Errorf("cfg = %+v", cfg)
	}
}

// TestWriterRoundTripYAML re-parses writer output with strict decoding, ensuring
// no unknown keys sneak in.
func TestWriterRoundTripYAML(t *testing.T) {
	p := writeFixture(t)
	if _, err := AddMachine(p, Machine{Name: "n2", Host: "h", Username: "u", Password: "p"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse(b, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Machines) != 3 {
		t.Errorf("machines = %d", len(cfg.Machines))
	}
}
