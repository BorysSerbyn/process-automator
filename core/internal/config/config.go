// Package config loads the app's config file over defaults supplied by plugins.
// It knows no specific service: server definitions come from plugins and the
// file, and each plugin's own settings live in its plugins.<name> section.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"

	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/yamlfile"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// Config is the whole config file.
type Config struct {
	// MCPServers are external MCP servers by name. Plugins provide defaults;
	// an entry here replaces the plugin's definition of the same name.
	MCPServers map[string]pluginapi.ServerSpec `yaml:"mcpServers"`
	// Plugins holds each plugin's own config section, decoded by the plugin.
	Plugins map[string]yaml.Node `yaml:"plugins"`
	// Settings are read and written by the settings store; listed so the
	// strict decoder accepts them.
	Settings yaml.Node `yaml:"settings"`
}

// DefaultPath is the per-user config file location.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, product.Folder, "config.yaml"), nil
}

// Load reads the config file at path over base (server definitions from plugins).
// A missing file yields base. Servers named in the file replace base entries entirely.
func Load(path string, base map[string]pluginapi.ServerSpec) (Config, error) {
	cfg := Config{MCPServers: map[string]pluginapi.ServerSpec{}, Plugins: map[string]yaml.Node{}}
	for name, s := range base {
		cfg.MCPServers[name] = s
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, cfg.Validate()
	}
	if err != nil {
		return cfg, err
	}
	// Strict: a misspelled or retired field (e.g. a server's old envSecrets) is
	// an error, not silently ignored.
	var file Config
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&file); err != nil && !errors.Is(err, io.EOF) {
		return cfg, fmt.Errorf("config %s: %w", path, err)
	}
	for name, s := range file.MCPServers {
		cfg.MCPServers[name] = s
	}
	for name, n := range file.Plugins {
		cfg.Plugins[name] = n
	}
	return cfg, cfg.Validate()
}

// Validate checks the config is usable.
func (c Config) Validate() error {
	var errs []error
	for name, s := range c.MCPServers {
		errs = append(errs, s.Validate(name))
	}
	return errors.Join(errs...)
}

// DecodePlugin decodes plugins.<name> into v; absent fields keep v's values and
// unknown fields are an error (typos).
func (c Config) DecodePlugin(name string, v any) error {
	n, ok := c.Plugins[name]
	if !ok {
		return nil
	}
	raw, err := yaml.Marshal(&n)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("config plugins.%s: %w", name, err)
	}
	return nil
}

// serversKey is the config file's section of external MCP servers.
const serversKey = "mcpServers"

// SetServer writes a server to the file's mcpServers section after validating
// it, replacing an entry of the same name (and, for a built-in preset's name,
// overriding the preset). The rest of the file is preserved.
func SetServer(path, name string, s pluginapi.ServerSpec) error {
	if err := s.Validate(name); err != nil {
		return err
	}
	return yamlfile.EditSection(path, serversKey, func(section *yaml.Node) error {
		var val yaml.Node
		if err := val.Encode(s); err != nil {
			return err
		}
		yamlfile.Set(section, name, &val)
		return nil
	})
}

// RemoveServer deletes a server from the file's mcpServers section; it
// reports whether the file had it.
func RemoveServer(path, name string) (bool, error) {
	removed := false
	err := yamlfile.EditSection(path, serversKey, func(section *yaml.Node) error {
		removed = yamlfile.Delete(section, name)
		return nil
	})
	return removed, err
}
