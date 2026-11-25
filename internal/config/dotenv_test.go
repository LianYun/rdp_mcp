package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDotEnvReload(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "configs.yaml")
	env := filepath.Join(dir, ".env")
	t.Setenv("REAL_ENV", "from-process")
	for _, k := range []string{"A_PW", "B_PW"} {
		os.Unsetenv(k)
		t.Cleanup(func() { os.Unsetenv(k) })
	}
	t.Cleanup(func() { dotenvMu.Lock(); dotenvKeys = map[string]bool{}; dotenvMu.Unlock() })

	os.WriteFile(env, []byte("A_PW=one\nB_PW='two # x'\nREAL_ENV=from-file\n"), 0o600)
	LoadDotEnv(cfgPath, dir)
	if os.Getenv("A_PW") != "one" || os.Getenv("B_PW") != "two # x" || os.Getenv("REAL_ENV") != "from-process" {
		t.Fatalf("first load: %q %q %q", os.Getenv("A_PW"), os.Getenv("B_PW"), os.Getenv("REAL_ENV"))
	}
	// Edit: change A, drop B. The real environment still wins.
	os.WriteFile(env, []byte("A_PW=uno\nREAL_ENV=changed\n"), 0o600)
	LoadDotEnv(cfgPath, dir)
	if os.Getenv("A_PW") != "uno" || os.Getenv("REAL_ENV") != "from-process" {
		t.Fatalf("reload: %q %q", os.Getenv("A_PW"), os.Getenv("REAL_ENV"))
	}
	if _, ok := os.LookupEnv("B_PW"); ok {
		t.Fatal("removed key still set")
	}
}

func TestResolvePasswordUnsetVar(t *testing.T) {
	os.Unsetenv("NOPE_PW")
	_, err := ResolvePassword(context.Background(), Machine{Name: "m", Password: "${NOPE_PW}"}, nil)
	if err == nil || !strings.Contains(err.Error(), "NOPE_PW") || !strings.Contains(err.Error(), ".env") {
		t.Fatalf("want a clear error, got %v", err)
	}
	t.Setenv("NOPE_PW", "")
	if _, err := ResolvePassword(context.Background(), Machine{Name: "m", Password: "${NOPE_PW}"}, nil); err == nil {
		t.Fatal("empty variable accepted")
	}
	t.Setenv("NOPE_PW", "s3")
	if pw, err := ResolvePassword(context.Background(), Machine{Name: "m", Password: "x${NOPE_PW}"}, nil); err != nil || pw != "xs3" {
		t.Fatalf("got %q %v", pw, err)
	}
}
