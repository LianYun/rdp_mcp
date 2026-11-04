package session

import (
	"path/filepath"
	"testing"
)

func TestNormalizeFingerprint(t *testing.T) {
	a := NormalizeFingerprint("sha256:AB:CD:EF")
	b := NormalizeFingerprint("ab:cd:ef")
	if a != "abcdef" || b != "abcdef" {
		t.Fatal(a, b)
	}
}

func TestKnownHosts(t *testing.T) {
	k := NewKnownHosts(filepath.Join(t.TempDir(), "known_hosts"))
	if _, ok := k.Get("h:1"); ok {
		t.Fatal("unexpected")
	}
	if err := k.Set("h:1", "AA:BB"); err != nil {
		t.Fatal(err)
	}
	if fp, ok := k.Get("h:1"); !ok || fp != "aabb" {
		t.Fatal(fp, ok)
	}
	if err := k.Delete("h:1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := k.Get("h:1"); ok {
		t.Fatal("not deleted")
	}
}
