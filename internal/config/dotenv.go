package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LoadDotEnv loads KEY=VALUE pairs from .env files next to the config file and in the data
// directory. Variables already set in the process environment win. Returns the files loaded
// and warnings (e.g. permissive file modes).
func LoadDotEnv(configPath, dataDir string) (loaded []string, warnings []string) {
	seen := map[string]bool{}
	for _, dir := range []string{filepath.Dir(configPath), dataDir} {
		p := filepath.Join(dir, ".env")
		abs, _ := filepath.Abs(p)
		if seen[abs] {
			continue
		}
		seen[abs] = true
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			continue
		}
		if st.Mode().Perm()&0o077 != 0 {
			warnings = append(warnings, fmt.Sprintf("%s is readable by other users (mode %o); run: chmod 600 %s", p, st.Mode().Perm(), p))
		}
		if err := applyDotEnv(p); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", p, err))
			continue
		}
		loaded = append(loaded, p)
	}
	return loaded, warnings
}

func applyDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("line %d: expected KEY=VALUE", n)
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		} else if i := strings.Index(v, " #"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		if _, exists := os.LookupEnv(k); exists {
			continue
		}
		if err := os.Setenv(k, v); err != nil {
			return fmt.Errorf("line %d: %v", n, err)
		}
	}
	return sc.Err()
}
