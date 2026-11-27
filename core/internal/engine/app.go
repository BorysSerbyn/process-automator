// Package engine is the core behind the procauto commands: it loads the
// config and plugins, resolves routines' settings and readiness, runs them
// (taking turns, honouring trust and the LLM budgets, recording run status,
// history and failure notifications) and runs the scheduler. It knows nothing
// of the command line; internal/cli is its frontend.
package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BorysSerbyn/process-automator/core/internal/config"
	"github.com/BorysSerbyn/process-automator/core/internal/filelock"
	"github.com/BorysSerbyn/process-automator/core/internal/mcp"
	"github.com/BorysSerbyn/process-automator/core/internal/memory"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/secrets"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/yamlfile"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// App is the engine on one data folder: the config, the plugins and the
// connections to their servers. Commands load one when they need it.
type App struct {
	Config    config.Config
	Layout    Layout
	Connector *mcp.Connector
	// Plugins are the app's plugins and the accounts of them (each a plugin
	// of its own, under the account's name).
	Plugins []pluginapi.Plugin
	// Accounts are the accounts of plugins and of added servers: the
	// account's name, by the one it's an account of.
	Accounts map[string]string
	// builtin are the plugins the app was loaded with, without accounts.
	builtin []pluginapi.Plugin
}

// AnthropicKey is the secret-store key of the Anthropic API key.
const AnthropicKey = "anthropic-api-key"

// ClaudeCodeTokenKey holds an optional `claude setup-token` token for the
// claude-code backend (otherwise Claude Code's own login is used).
const ClaudeCodeTokenKey = "claude-code-oauth-token"

// Load loads the config at path over the plugins' defaults and wires the
// plugins it's given (the app's, the ones every app ships with included: the
// engine adds none); secrets is where secrets are kept.
func Load(path string, plugins []pluginapi.Plugin, secrets secrets.Store) (*App, error) {
	files := LayoutOf(path)
	base := map[string]pluginapi.ServerSpec{}
	for _, p := range plugins {
		if p.External != nil {
			base[p.Name] = *p.External
		}
	}
	cfg, err := config.Load(path, base)
	if err != nil {
		return nil, err
	}
	for _, p := range plugins {
		if _, clash := cfg.MCPServers[p.Name]; clash && p.InProcess != nil {
			return nil, fmt.Errorf("config mcpServers.%s clashes with the in-process %s plugin; use another name", p.Name, p.Name)
		}
	}
	builtin := plugins
	if plugins, err = resolveAccounts(&cfg, plugins); err != nil {
		return nil, err
	}
	conn := &mcp.Connector{
		Servers:  cfg.MCPServers,
		Secrets:  secrets,
		Name:     product.Command,
		Version:  product.Version,
		Builtins: map[string]func(mcp.Run) (*sdk.Server, error){},
		LockDir:  files.Locks(),
	}
	for _, p := range plugins {
		if p.InProcess == nil {
			continue
		}
		dataDir := files.PluginData(p.Name)
		conn.Builtins[p.Name] = func(run mcp.Run) (*sdk.Server, error) {
			return p.InProcess.Serve(&host{plugin: p, cfg: cfg, conn: conn, dataDir: dataDir, config: path, files: files, run: run})
		}
	}
	a := &App{Config: cfg, Layout: files, Connector: conn, Plugins: plugins, Accounts: cfg.Accounts, builtin: builtin}
	conn.Before = a.beforeConnect
	return a, nil
}

// resolveAccounts makes each account in cfg a plugin or server of its own:
// an account of a plugin is that plugin under the account's name (its
// description, Prepare and permissions; an external one's server reads the
// account's own secrets: config.AccountSpec), and one of an added server is
// that server, likewise. An in-process account reads its own config
// section, or its plugin's while it has none. It returns the plugins with
// the accounts of them added.
func resolveAccounts(cfg *config.Config, plugins []pluginapi.Plugin) ([]pluginapi.Plugin, error) {
	out := slices.Clone(plugins)
	names := make([]string, 0, len(cfg.Accounts))
	for name := range cfg.Accounts {
		names = append(names, name)
	}
	slices.Sort(names)
	var errs []error
	for _, name := range names {
		of := cfg.Accounts[name]
		bad := func(format string, args ...any) {
			errs = append(errs, fmt.Errorf("config accounts.%s: "+format, append([]any{name}, args...)...))
		}
		if slices.ContainsFunc(plugins, func(p pluginapi.Plugin) bool { return p.Name == name }) {
			bad("%s is a plugin's name; use another", name)
			continue
		}
		if base, ok := cfg.Accounts[of]; ok {
			bad("%s is an account itself (of %s); make it an account of %s", of, base, base)
			continue
		}
		i := slices.IndexFunc(plugins, func(p pluginapi.Plugin) bool { return p.Name == of })
		spec, server := cfg.MCPServers[of]
		switch {
		case i < 0 && !server:
			bad("no plugin %q", of)
			continue
		case i >= 0 && plugins[i].InProcess != nil:
			p := plugins[i]
			p.Name = name
			out = append(out, p)
			if _, own := cfg.Plugins[name]; !own {
				if section, ok := cfg.Plugins[of]; ok {
					cfg.Plugins[name] = section
				}
			}
			continue
		}
		acct := config.AccountSpec(name, spec)
		cfg.MCPServers[name] = acct
		if perms, ok := cfg.Permissions[of]; ok {
			cfg.Permissions[name] = slices.Clone(perms)
		}
		if i >= 0 {
			p := plugins[i]
			p.Name, p.External = name, &acct
			out = append(out, p)
		}
	}
	return out, errors.Join(errs...)
}

// AccountOf is the plugin (or added server) an account is an account of;
// ok is false for a name that isn't an account.
func (a *App) AccountOf(name string) (of string, ok bool) {
	of, ok = a.Accounts[name]
	return of, ok
}

// AccountsOf are the accounts of a plugin or added server, sorted.
func (a *App) AccountsOf(name string) []string {
	var out []string
	for acct, of := range a.Accounts {
		if of == name {
			out = append(out, acct)
		}
	}
	slices.Sort(out)
	return out
}

// LoadForEdit takes the edit lock, then loads the app (so its config is read
// under the lock). Commands that change routines or plugins hold it for the
// whole change: their check-then-write steps ("create unless it exists",
// "delete the file, then its settings") stay whole when frontends, tool calls
// and terminals change things at the same time. unlock releases it.
func LoadForEdit(path string, plugins []pluginapi.Plugin, secrets secrets.Store) (a *App, unlock func(), err error) {
	files := LayoutOf(path)
	if err := os.MkdirAll(files.Data(), 0o700); err != nil {
		return nil, nil, err
	}
	unlock, err = filelock.Lock(files.EditLock(), true)
	if err != nil {
		return nil, nil, err
	}
	if a, err = Load(path, plugins, secrets); err != nil {
		unlock()
		return nil, nil, err
	}
	// Before any change: routines from before trust existed get trusted as
	// they are, never ones made or changed from here on.
	if err := a.InitTrust(); err != nil {
		unlock()
		return nil, nil, err
	}
	return a, unlock, nil
}

// Servers is every server a routine reaches: its own, and those its plugins
// connect to (pluginapi.InProcess.Uses), followed through.
func (a *App) Servers(r routines.Routine) []string {
	out := slices.Clone(r.Servers)
	for i := 0; i < len(out); i++ { // out grows as plugins add theirs
		if p, ok := a.InProcess(out[i]); ok && p.InProcess.Uses != nil {
			for _, s := range p.InProcess.Uses(a.pluginHost(p)) {
				if s != "" && !slices.Contains(out, s) {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// KnownServer reports whether a server name is an in-process plugin or a
// configured server (what a routine's automator.servers may name).
func (a *App) KnownServer(name string) bool {
	_, builtin := a.Connector.Builtins[name]
	_, server := a.Connector.Servers[name]
	return builtin || server
}

// pluginHost is a plugin's host outside any run (for reading its config).
func (a *App) pluginHost(p pluginapi.Plugin) *host {
	return &host{plugin: p, cfg: a.Config, conn: a.Connector, dataDir: a.Layout.PluginData(p.Name), config: a.Layout.Config(), files: a.Layout}
}

// host is the engine side of pluginapi.Host for one in-process plugin.
// One is made per session, so it knows the run the session is for.
type host struct {
	plugin  pluginapi.Plugin
	cfg     config.Config
	conn    *mcp.Connector
	dataDir string
	config  string // the config file, for ConnectApp
	files   Layout // the app's files, for Notify
	run     mcp.Run
}

// RunOf is the run plugin sessions serve for r working in folder; id is the
// run's history ID ("" when the routine is being checked, not run).
func RunOf(r routines.Routine, folder, id string) mcp.Run {
	return mcp.Run{Run: pluginapi.Run{Routine: r.Name, ID: id, Folder: folder}, Plugins: r.Plugins}
}

func (h *host) DataDir() (string, error) {
	if err := os.MkdirAll(h.dataDir, 0o700); err != nil {
		return "", err
	}
	return h.dataDir, nil
}

// Run is the run without the routine's plugin sections: each plugin reads
// its own (RoutineConfig).
func (h *host) Run() pluginapi.Run { return h.run.Run }

func (h *host) RoutineConfig(v any) error {
	section, ok := h.run.Plugins[h.plugin.Name]
	if !ok {
		return nil
	}
	if err := yamlfile.DecodeStrict(section, v); err != nil {
		return fmt.Errorf("automator.plugins.%s: %w", h.plugin.Name, err)
	}
	return nil
}

// Connect opens a session to another plugin for this one's own use: it isn't
// part of the routine's run (the routine didn't list it, and its calls don't
// go through the run's gateway), so it serves no run.
func (h *host) Connect(ctx context.Context, name string) (*mcp.Session, error) {
	return h.conn.Connect(ctx, name)
}

// AppServerCommand is the command ConnectApp starts: this executable serving
// the app's MCP server, read-only, on the config at path. Tests replace it.
var AppServerCommand = func(path string) (*exec.Cmd, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return exec.Command(exe, "--config", path, "mcp", "serve", "--read-only"), nil
}

// ConnectApp opens a session to the app's own MCP server, read-only, over
// stdio. Like Connect, it serves no run.
func (h *host) ConnectApp(ctx context.Context) (*mcp.Session, error) {
	cmd, err := AppServerCommand(h.config)
	if err != nil {
		return nil, err
	}
	client := sdk.NewClient(&sdk.Implementation{Name: h.conn.Name, Version: h.conn.Version}, nil)
	s, err := client.Connect(ctx, &sdk.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to the app's MCP server: %w", err)
	}
	return s, nil
}

func (h *host) Config(v any) error { return h.cfg.DecodePlugin(h.plugin.Name, v) }

func (h *host) AppName() string { return product.Name }

// Secret returns a secret the plugin declared; undeclared names are refused so a
// plugin can only read its own secrets.
func (h *host) Secret(name string) (string, error) {
	for _, s := range h.plugin.InProcess.Secrets {
		if s.Name == name {
			v, err := h.conn.Secrets.Get(PluginSecretKey(h.plugin.Name, name))
			if errors.Is(err, secrets.ErrNotFound) {
				return "", &mcp.NeedsReauthError{Server: h.plugin.Name, Cause: fmt.Errorf("secret %q not set", name)}
			}
			return v, err
		}
	}
	return "", fmt.Errorf("plugin %s didn't declare secret %q", h.plugin.Name, name)
}

// Settings are the app-wide settings' effective values, read from the
// config file now.
func (a *App) Settings() (settings.Values, error) {
	return (settings.Store{Path: a.Layout.Config()}).Values()
}

// PluginSecretKey is the secrets-store key of an in-process plugin's
// secret: namespaced by plugin, unlike the keys external servers reference.
func PluginSecretKey(plugin, name string) string { return "plugin/" + plugin + "/" + name }

// InProcess returns an in-process plugin by name.
func (a *App) InProcess(name string) (pluginapi.Plugin, bool) {
	p, ok := a.FindPlugin(name)
	return p, ok && p.InProcess != nil
}

// FindPlugin returns an installed plugin by name.
func (a *App) FindPlugin(name string) (pluginapi.Plugin, bool) {
	for _, p := range a.Plugins {
		if p.Name == name {
			return p, true
		}
	}
	return pluginapi.Plugin{}, false
}

// Memories is the store of what routines are told to remember.
func (a *App) Memories() memory.Store {
	return memory.Store{Dir: a.Layout.Memories()}
}
