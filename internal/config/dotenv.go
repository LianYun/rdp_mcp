package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Keys this process set from .env files, so a reload can update or remove them without
// overriding variables that came from the real process environment.
var (
	dotenvMu   sync.Mutex
	dotenvKeys = map[string]bool{}
)

// DotEnvFiles returns the .env paths that LoadDotEnv reads, in priority order.
func DotEnvFiles(configPath, dataDir string) []string {
	var out []string
	seen := map[string]bool{}
	for _, dir := range []string{filepath.Dir(configPath), dataDir} {
		p := filepath.Join(dir, ".env")
		abs, _ := filepath.Abs(p)
		if !seen[abs] {
			seen[abs] = true
			out = append(out, p)
		}
	}
	return out
}

// LoadDotEnv loads KEY=VALUE pairs from .env files next to the config file and in the data
// directory (the first file wins for duplicate keys). Variables set in the real process
// environment win. Calling it again applies edits: changed values are updated and keys
// removed from the files are unset. Returns the files loaded and warnings.
func LoadDotEnv(configPath, dataDir string) (loaded []string, warnings []string) {
	dotenvMu.Lock()
	defer dotenvMu.Unlock()
	vals := map[string]string{}
	for _, p := range DotEnvFiles(configPath, dataDir) {
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			continue
		}
		if st.Mode().Perm()&0o077 != 0 {
			warnings = append(warnings, fmt.Sprintf("%s is readable by other users (mode %o); run: chmod 600 %s", p, st.Mode().Perm(), p))
		}
		kv, err := parseDotEnv(p)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", p, err))
			continue
		}
		for k, v := range kv {
			if _, ok := vals[k]; !ok {
				vals[k] = v
			}
		}
		loaded = append(loaded, p)
	}
	for k, v := range vals {
		if _, exists := os.LookupEnv(k); exists && !dotenvKeys[k] {
			continue // real environment wins
		}
		if err := os.Setenv(k, v); err != nil {
			warnings = append(warnings, fmt.Sprintf(".env %s: %v", k, err))
			continue
		}
		dotenvKeys[k] = true
	}
	for k := range dotenvKeys {
		if _, ok := vals[k]; !ok {
			_ = os.Unsetenv(k)
			delete(dotenvKeys, k)
		}
	}
	return loaded, warnings
}

func parseDotEnv(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]string{}
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
			return nil, fmt.Errorf("line %d: expected KEY=VALUE", n)
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		} else if i := strings.Index(v, " #"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		if _, dup := out[k]; !dup {
			out[k] = v
		}
	}
	return out, sc.Err()
}
