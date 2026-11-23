// Package config loads the app's config file over defaults supplied by plugins.
// It knows no specific service: server definitions come from plugins and the
// file, and each plugin's own settings live in its plugins.<name> section.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"

	"github.com/BorysSerbyn/process-automator/core/internal/permissions"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/yamlfile"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// Config is the whole config file, over the plugins' defaults.
type Config struct {
	// MCPServers are external MCP servers by name. Plugins provide defaults;
	// an entry here replaces the plugin's definition of the same name.
	MCPServers map[string]pluginapi.ServerSpec
	// Permissions are what the OS must allow each server the file defines
	// (mcpServers.<name>.permissions): every server the file has is here,
	// with none when it declares none; a plugin's preset isn't.
	Permissions map[string][]string
	// Plugins holds each plugin's own config section, decoded by the plugin.
	Plugins map[string]yaml.Node
	// Accounts are second sign-ins of plugins under names of their own
	// (accounts.<name>.of): the account's name, by the plugin it's an account
	// of. The engine resolves them; an external one's server is here, in
	// MCPServers, once Load has resolved it (AccountSpec).
	Accounts map[string]string
}

// Account is an accounts.<name> entry: the plugin it's an account of.
type Account struct {
	Of string `yaml:"of"`
}

// file is the config file as written, decoded strictly.
type file struct {
	MCPServers map[string]Server    `yaml:"mcpServers"`
	Plugins    map[string]yaml.Node `yaml:"plugins"`
	Accounts   map[string]Account   `yaml:"accounts"`
	// Settings are read and written by the settings store; listed so the
	// strict decoder accepts them.
	Settings yaml.Node `yaml:"settings"`
}

// Server is an mcpServers.<name> entry: the server, and what the OS must
// allow it before its tools can work.
type Server struct {
	pluginapi.ServerSpec `yaml:",inline"`
	Permissions          []string `yaml:"permissions,omitempty"`
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
	cfg := Config{MCPServers: map[string]pluginapi.ServerSpec{}, Permissions: map[string][]string{}, Plugins: map[string]yaml.Node{}, Accounts: map[string]string{}}
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
	var f file
	if err := yamlfile.DecodeStrict(raw, &f); err != nil {
		return cfg, fmt.Errorf("config %s: %w", path, err)
	}
	var errs []error
	for name, s := range f.MCPServers {
		cfg.MCPServers[name] = s.ServerSpec
		perms, err := permissions.Check(fmt.Sprintf("config mcpServers.%s", name), s.Permissions)
		errs = append(errs, err)
		cfg.Permissions[name] = perms
	}
	for name, n := range f.Plugins {
		cfg.Plugins[name] = n
	}
	for name, acct := range f.Accounts {
		switch {
		case !ValidName(name):
			errs = append(errs, fmt.Errorf("config accounts.%s: names are lowercase letters, digits and dashes", name))
		case acct.Of == "":
			errs = append(errs, fmt.Errorf("config accounts.%s: of: names the plugin it's an account of", name))
		case hasKey(f.MCPServers, name):
			errs = append(errs, fmt.Errorf("config accounts.%s clashes with mcpServers.%s; use another name", name, name))
		}
		cfg.Accounts[name] = acct.Of
	}
	return cfg, errors.Join(append(errs, cfg.Validate())...)
}

// Validate checks the config is usable.
func (c Config) Validate() error {
	var errs []error
	for name, s := range c.MCPServers {
		errs = append(errs, ValidateServer(name, s))
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
	if err := yamlfile.DecodeStrict(&n, v); err != nil {
		return fmt.Errorf("config plugins.%s: %w", name, err)
	}
	return nil
}

func hasKey[V any](m map[string]V, k string) bool {
	_, ok := m[k]
	return ok
}

// serversKey is the config file's section of external MCP servers;
// accountsKey its section of accounts.
const (
	serversKey  = "mcpServers"
	accountsKey = "accounts"
)

// SetAccount writes accounts.<name>: an account of the plugin of. The rest
// of the file is preserved.
func SetAccount(path, name, of string) error {
	return yamlfile.EditSection(path, accountsKey, func(section *yaml.Node) error {
		var val yaml.Node
		if err := val.Encode(Account{Of: of}); err != nil {
			return err
		}
		yamlfile.Set(section, name, &val)
		return nil
	})
}

// RemoveAccount deletes accounts.<name>; it reports whether the file had it.
func RemoveAccount(path, name string) (bool, error) {
	removed := false
	err := yamlfile.EditSection(path, accountsKey, func(section *yaml.Node) error {
		removed = yamlfile.Delete(section, name)
		return nil
	})
	return removed, err
}

// SetServer writes a server to the file's mcpServers section after validating
// it, replacing an entry of the same name (and, for a built-in preset's name,
// overriding the preset). The rest of the file is preserved.
func SetServer(path, name string, s Server) error {
	if err := ValidateServer(name, s.ServerSpec); err != nil {
		return err
	}
	var err error
	if s.Permissions, err = permissions.Check("permissions", s.Permissions); err != nil {
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
