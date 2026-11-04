package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateLegacyDataDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := filepath.Join(home, LegacyDirName)
	_ = os.MkdirAll(filepath.Join(old, "logs"), 0o700)
	_ = os.WriteFile(filepath.Join(old, FileName), []byte("machines: []\n"), 0o600)
	_ = os.WriteFile(filepath.Join(old, ".env"), []byte("PW=x\n"), 0o600)

	note, err := MigrateLegacyDataDir()
	if err != nil || !strings.Contains(note, "configs.yaml") {
		t.Fatalf("note=%q err=%v", note, err)
	}
	for _, f := range []string{FileName, ".env"} {
		st, err := os.Stat(filepath.Join(home, DirName, f))
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("%s not migrated: %v", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(old, FileName)); err != nil {
		t.Fatal("legacy dir must be left in place")
	}
	// second run is a no-op
	if note, err := MigrateLegacyDataDir(); note != "" || err != nil {
		t.Fatalf("second run: %q %v", note, err)
	}
}

func TestMigrateNothingToDo(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if note, err := MigrateLegacyDataDir(); note != "" || err != nil {
		t.Fatalf("%q %v", note, err)
	}
}
