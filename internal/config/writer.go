package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// The service is the only writer of configs.yaml. These helpers mutate the YAML tree
// (yaml.v3 Node API) instead of re-marshaling the struct, so comments, ordering and
// unknown-but-valid formatting survive every edit.

// loadRoot reads the YAML file at path into its document root. A missing file yields
// a fresh empty mapping. The second return value reports whether the file existed.
func loadRoot(path string) (*yaml.Node, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, false, fmt.Errorf("parse %s: %w", path, err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}, true, nil
	}
	if doc.Content[0].Kind != yaml.MappingNode {
		return nil, true, fmt.Errorf("%s: top level must be a mapping", path)
	}
	return &doc, true, nil
}

// saveRoot validates and atomically writes the document back to path.
func saveRoot(path string, doc *yaml.Node) (*Config, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2) // match the project's YAML style
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	out := buf.Bytes()
	cfg, err := Parse(out, path)
	if err != nil {
		return nil, err
	}
	if _, err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return nil, err
	}
	return cfg, os.Rename(tmp, path)
}

// mapGet returns the value node for key in a mapping node (nil if absent).
func mapGet(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// mapSet stores a scalar (or flow sequence of scalars) for key, preserving the
// comment attached to an existing key node.
func mapSet(m *yaml.Node, key string, value any) error {
	vn, err := valueNode(value)
	if err != nil {
		return err
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			old := m.Content[i+1]
			// Keep comments attached to the old value node (`key: value # note`).
			vn.HeadComment, vn.LineComment, vn.FootComment = old.HeadComment, old.LineComment, old.FootComment
			*old = *vn
			return nil
		}
	}
	kn := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	m.Content = append(m.Content, kn, vn)
	return nil
}

// mapDelete removes key from a mapping node.
func mapDelete(m *yaml.Node, key string) {
	if m == nil || m.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

// valueNode marshals v into a node. Scalars and flat sequences are supported;
// mappings are rejected because config writes never nest them implicitly.
func valueNode(v any) (*yaml.Node, error) {
	b, err := yaml.Marshal(v)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return nil, fmt.Errorf("cannot encode %v as a YAML value", v)
	}
	n := doc.Content[0]
	if n.Kind == yaml.MappingNode {
		return nil, fmt.Errorf("unexpected mapping value for %v", v)
	}
	return n, nil
}

// entrySpec describes one machine field for node syncing: set means present in the
// incoming definition (unset fields are removed from the YAML entry).
type entrySpec struct {
	key string
	set bool
	val any
}

// machineSpecs lists the writable machine fields in config-file order, excluding
// password/password_cmd (dedicated flows) and tags (handled separately for style).
func machineSpecs(m Machine) []entrySpec {
	sp := []entrySpec{
		{"name", true, m.Name},
		{"description", m.Description != "", m.Description},
		{"host", true, m.Host},
		{"username", true, m.Username},
		{"domain", m.Domain != "", m.Domain},
		{"cert_fingerprint", m.CertFingerprint != "", m.CertFingerprint},
		{"port", m.Port != 0, m.Port},
		{"width", m.Width != 0, m.Width},
		{"height", m.Height != 0, m.Height},
		{"security", m.Security != "", m.Security},
		{"cert", m.Cert != "", m.Cert},
	}
	opt := func(key string, b *bool) {
		var v any
		if b != nil {
			v = *b
		}
		sp = append(sp, entrySpec{key, b != nil, v})
	}
	opt("enabled", m.Enabled)
	opt("auto_connect", m.AutoConnect)
	opt("enable_drive", m.EnableDrive)
	opt("enable_clipboard", m.EnableClipboard)
	opt("disable_wallpaper", m.DisableWallpaper)
	var idle any
	if m.IdleDisconnect != nil {
		idle = m.IdleDisconnect
	}
	sp = append(sp, entrySpec{"idle_disconnect", m.IdleDisconnect != nil, idle})
	var attempts any
	if m.ReconnectAttempts != nil {
		attempts = *m.ReconnectAttempts
	}
	sp = append(sp, entrySpec{"reconnect_attempts", m.ReconnectAttempts != nil, attempts})
	return sp
}

// syncMachine rewrites the mapping entry so it contains exactly the fields of m.
// Comments on retained keys stay; ordering of existing keys is preserved.
func syncMachine(entry *yaml.Node, m Machine) error {
	if entry.Kind != yaml.MappingNode {
		// The entry was not a mapping (e.g. a stray scalar); replace it wholesale.
		n, err := machineNode(m)
		if err != nil {
			return err
		}
		*entry = *n
		return nil
	}
	for _, sp := range machineSpecs(m) {
		if !sp.set {
			mapDelete(entry, sp.key)
			continue
		}
		if err := mapSet(entry, sp.key, sp.val); err != nil {
			return err
		}
	}
	mapDelete(entry, "tags")
	if len(m.Tags) > 0 {
		if err := mapSet(entry, "tags", m.Tags); err != nil {
			return err
		}
		if tn := mapGet(entry, "tags"); tn != nil && tn.Kind == yaml.SequenceNode {
			tn.Style = yaml.FlowStyle
		}
	}
	return nil
}

// machineNode builds a fresh mapping node for m.
func machineNode(m Machine) (*yaml.Node, error) {
	var wrap struct {
		M Machine `yaml:"m"`
	}
	wrap.M = m
	b, err := yaml.Marshal(&wrap) // {"m": {...}} so the machine is the value node
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	entry := doc.Content[0].Content[1]
	entry.Style = 0
	return entry, nil
}

// isNullNode reports whether n represents a YAML null (e.g. an empty `machines:` key).
func isNullNode(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && (n.Tag == "!!null" || n.Value == "~" || n.Value == "")
}

// machinesSeq returns the machines sequence node, creating it (and the machines key)
// when absent.
func machinesSeq(root *yaml.Node) (*yaml.Node, error) {
	top := root.Content[0]
	v := mapGet(top, "machines")
	if v != nil && isNullNode(v) {
		*v = yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		return v, nil
	}
	if v == nil {
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		top.Content = append(top.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "machines"}, seq)
		return seq, nil
	}
	if v.Kind != yaml.SequenceNode {
		return nil, errors.New("machines: expected a list")
	}
	return v, nil
}

// findMachine returns the sequence index of the machine with that name (-1 if absent).
func findMachine(seq *yaml.Node, name string) int {
	for i, e := range seq.Content {
		if n := mapGet(e, "name"); n != nil && n.Value == name {
			return i
		}
	}
	return -1
}

// AddMachine appends a machine. Fails if the name exists or the result is invalid.
func AddMachine(path string, m Machine) (*Config, error) {
	root, _, err := loadRoot(path)
	if err != nil {
		return nil, err
	}
	seq, err := machinesSeq(root)
	if err != nil {
		return nil, err
	}
	if findMachine(seq, m.Name) >= 0 {
		return nil, fmt.Errorf("machine %q already exists", m.Name)
	}
	entry, err := machineNode(m)
	if err != nil {
		return nil, err
	}
	seq.Content = append(seq.Content, entry)
	return saveRoot(path, root)
}

// UpdateMachine replaces the definition of name with m (renaming when m.Name differs).
func UpdateMachine(path, name string, m Machine) (*Config, error) {
	root, _, err := loadRoot(path)
	if err != nil {
		return nil, err
	}
	seq, err := machinesSeq(root)
	if err != nil {
		return nil, err
	}
	idx := findMachine(seq, name)
	if idx < 0 {
		return nil, os.ErrNotExist
	}
	if m.Name != name && findMachine(seq, m.Name) >= 0 {
		return nil, fmt.Errorf("machine %q already exists", m.Name)
	}
	if err := syncMachine(seq.Content[idx], m); err != nil {
		return nil, err
	}
	return saveRoot(path, root)
}

// DeleteMachine removes the machine from the file.
func DeleteMachine(path, name string) (*Config, error) {
	root, _, err := loadRoot(path)
	if err != nil {
		return nil, err
	}
	seq, err := machinesSeq(root)
	if err != nil {
		return nil, err
	}
	idx := findMachine(seq, name)
	if idx < 0 {
		return nil, os.ErrNotExist
	}
	seq.Content = append(seq.Content[:idx], seq.Content[idx+1:]...)
	return saveRoot(path, root)
}

// UpdateMachineFields sets or removes (nil value) individual scalar keys on the
// machine called name, leaving every other key and comment untouched. It is used by
// flows that must not rewrite the whole entry (e.g. password references).
func UpdateMachineFields(path, name string, set map[string]any) (*Config, error) {
	root, _, err := loadRoot(path)
	if err != nil {
		return nil, err
	}
	seq, err := machinesSeq(root)
	if err != nil {
		return nil, err
	}
	idx := findMachine(seq, name)
	if idx < 0 {
		return nil, os.ErrNotExist
	}
	entry := seq.Content[idx]
	for _, k := range sortedKeys(set) {
		v := set[k]
		if v == nil {
			mapDelete(entry, k)
			continue
		}
		if err := mapSet(entry, k, v); err != nil {
			return nil, err
		}
	}
	return saveRoot(path, root)
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// SetMCPToken writes server.mcp_token, preserving the rest of the file.
func SetMCPToken(path, token string) (*Config, error) {
	root, _, err := loadRoot(path)
	if err != nil {
		return nil, err
	}
	top := root.Content[0]
	srv := mapGet(top, "server")
	if srv == nil {
		srv = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		top.Content = append(top.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "server"}, srv)
	} else if srv.Kind != yaml.MappingNode {
		*srv = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	if token == "" {
		mapDelete(srv, "mcp_token")
	} else if err := mapSet(srv, "mcp_token", token); err != nil {
		return nil, err
	}
	return saveRoot(path, root)
}

// SaveRaw validates raw YAML and atomically writes it to path.
func SaveRaw(path string, raw []byte) (*Config, error) {
	cfg, err := Parse(raw, path)
	if err != nil {
		return nil, err
	}
	if _, err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return nil, err
	}
	return cfg, os.Rename(tmp, path)
}
