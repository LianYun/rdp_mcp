package session

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"
)

// KnownHosts stores trusted certificate fingerprints ("host:port fingerprint" per line).
type KnownHosts struct {
	path string
	mu   sync.Mutex
}

func NewKnownHosts(path string) *KnownHosts { return &KnownHosts{path: path} }

// NormalizeFingerprint makes fingerprints comparable ("sha256:AB:CD" == "abcd").
func NormalizeFingerprint(fp string) string {
	fp = strings.TrimSpace(strings.ToLower(fp))
	if i := strings.Index(fp, ":"); i > 0 && !isHexPair(fp[:i]) {
		fp = fp[i+1:] // strip algorithm prefix like "sha256:"
	}
	return strings.NewReplacer(":", "", " ", "").Replace(fp)
}

func isHexPair(s string) bool {
	if len(s) != 2 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

func (k *KnownHosts) load() (map[string]string, error) {
	m := map[string]string{}
	f, err := os.Open(k.path)
	if os.IsNotExist(err) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			m[parts[0]] = parts[1]
		}
	}
	return m, sc.Err()
}

func (k *KnownHosts) save(m map[string]string) error {
	var b strings.Builder
	b.WriteString("# rdp_mcp trusted RDP certificates: host:port sha256-fingerprint\n")
	for h, fp := range m {
		fmt.Fprintf(&b, "%s %s\n", h, fp)
	}
	tmp := k.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, k.path)
}

// Get returns the stored fingerprint for hostport.
func (k *KnownHosts) Get(hostport string) (string, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	m, err := k.load()
	if err != nil {
		return "", false
	}
	fp, ok := m[hostport]
	return fp, ok
}

func (k *KnownHosts) Set(hostport, fp string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	m, err := k.load()
	if err != nil {
		return err
	}
	m[hostport] = NormalizeFingerprint(fp)
	return k.save(m)
}

func (k *KnownHosts) Delete(hostport string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	m, err := k.load()
	if err != nil {
		return err
	}
	delete(m, hostport)
	return k.save(m)
}
