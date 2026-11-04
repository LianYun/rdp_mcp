package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// LegacyDirName is the data directory used before the project was renamed to remoter.
const LegacyDirName = ".rdp_mcp"

// legacyFiles are copied from the legacy data directory on first start.
var legacyFiles = []string{FileName, ".env", "known_hosts"}

// MigrateLegacyDataDir copies the user's configuration from ~/.rdp_mcp to ~/.remoter
// when ~/.remoter does not exist yet. The legacy directory is left untouched.
// It returns a human readable note when something was migrated.
func MigrateLegacyDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	newDir := filepath.Join(home, DirName)
	oldDir := filepath.Join(home, LegacyDirName)
	if _, err := os.Stat(newDir); err == nil {
		return "", nil // already migrated or freshly created
	}
	if st, err := os.Stat(oldDir); err != nil || !st.IsDir() {
		return "", nil // nothing to migrate
	}
	if err := os.MkdirAll(newDir, 0o700); err != nil {
		return "", err
	}
	var copied []string
	for _, name := range legacyFiles {
		src := filepath.Join(oldDir, name)
		if _, err := os.Stat(src); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err := copyFile0600(src, filepath.Join(newDir, name)); err != nil {
			return "", fmt.Errorf("migrate %s: %w", name, err)
		}
		copied = append(copied, name)
	}
	if len(copied) == 0 {
		return "", nil
	}
	return fmt.Sprintf("migrated %v from %s to %s (the old directory was left in place)", copied, oldDir, newDir), nil
}

func copyFile0600(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
