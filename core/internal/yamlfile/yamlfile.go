// Package yamlfile edits a YAML file in place: the file is loaded as a node
// tree, changed, and written back atomically, so everything else in it
// (other sections, comments, order) is preserved.
package yamlfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BorysSerbyn/process-automator/core/internal/atomicfile"
	"go.yaml.in/yaml/v3"
)

// EditSection applies fn to the top-level mapping under key (created if
// missing; created empty for a missing file) and writes the file back. Edits
// are serialized across processes (a lock file next to it), so concurrent
// writers (CLI, MCP tool calls, frontends) never lose each other's changes.
func EditSection(path, key string, fn func(section *yaml.Node) error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return atomicfile.Locked(path, func() error { return editSection(path, key, fn) })
}

func editSection(path, key string, fn func(section *yaml.Node) error) error {
	var doc yaml.Node
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return err
	default:
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("config %s: %w", path, err)
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("config %s: top level must be a mapping", path)
	}
	section := Get(root, key)
	if section == nil || section.Kind != yaml.MappingNode {
		section = &yaml.Node{Kind: yaml.MappingNode}
		Set(root, key, section)
	}
	if err := fn(section); err != nil {
		return err
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	return atomicfile.Write(path, out)
}

// Get is a mapping's value for key, or nil.
func Get(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// Set sets a mapping's value for key, appending the key if it's new.
func Set(m *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, val)
}

// Delete removes key from a mapping; it reports whether it was there.
func Delete(m *yaml.Node, key string) bool {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return true
		}
	}
	return false
}
