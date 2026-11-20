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
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// App is the engine on one data folder: the config, the plugins and the
// connections to their servers. Commands load one when they need it.
type App struct {
	Config    config.Config
	Layout    Layout
	Connector *mcp.Connector
	Plugins   []pluginapi.Plugin
}

// AnthropicKey is the secret-store key of the Anthropic API key.
const AnthropicKey = "anthropic-api-key"

// ClaudeCodeTokenKey holds an optional `claude setup-token` token for the
// claude-code backend (otherwise Claude Code's own login is used).
const ClaudeCodeTokenKey = "claude-code-oauth-token"

// Load loads the config at path over the plugins' defaults and wires the
// plugins, adding the core's own notify plugin; secrets is where secrets are
// kept.
func Load(path string, plugins []pluginapi.Plugin, secrets secrets.Store) (*App, error) {
	files := LayoutOf(path)
	// Callers may pass an app's plugins back in (the daemon reloads that way),
	// so notify is added to the given plugins without one already there.
	plugins = slices.DeleteFunc(slices.Clone(plugins), func(p pluginapi.Plugin) bool { return p.Name == NotifyServer })
	plugins = append(plugins, notifyPlugin(files))
	if err := pluginapi.Validate(plugins); err != nil {
		return nil, err
	}
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
		if _, clash := cfg.MCPServers[p.Name]; clash && p.Serve != nil {
			return nil, fmt.Errorf("config mcpServers.%s clashes with the in-process %s plugin; use another name", p.Name, p.Name)
		}
	}
	conn := &mcp.Connector{
		Servers:  cfg.MCPServers,
		Secrets:  secrets,
		Name:     product.Command,
		Version:  product.Version,
		Builtins: map[string]func(pluginapi.Run) (*sdk.Server, error){},
		LockDir:  files.Locks(),
	}
	for _, p := range plugins {
		if p.Serve == nil {
			continue
		}
		dataDir := files.PluginData(p.Name)
		conn.Builtins[p.Name] = func(run pluginapi.Run) (*sdk.Server, error) {
			return p.Serve(&host{plugin: p, cfg: cfg, conn: conn, dataDir: dataDir, run: run})
		}
	}
	return &App{Config: cfg, Layout: files, Connector: conn, Plugins: plugins}, nil
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
// connect to (pluginapi.Plugin.Uses), followed through.
func (a *App) Servers(r routines.Routine) []string {
	out := slices.Clone(r.Servers)
	for i := 0; i < len(out); i++ { // out grows as plugins add theirs
		for _, p := range a.Plugins {
			if p.Name != out[i] || p.Uses == nil {
				continue
			}
			for _, s := range p.Uses(a.pluginHost(p)) {
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
	return &host{plugin: p, cfg: a.Config, conn: a.Connector, dataDir: a.Layout.PluginData(p.Name)}
}

// host is the engine side of pluginapi.Host for one in-process plugin.
// One is made per session, so it knows the run the session is for.
type host struct {
	plugin  pluginapi.Plugin
	cfg     config.Config
	conn    *mcp.Connector
	dataDir string
	run     pluginapi.Run
}

// RunOf is the run plugin sessions serve for r working in folder; id is the
// run's history ID ("" when the routine is being checked, not run).
func RunOf(r routines.Routine, folder, id string) pluginapi.Run {
	return pluginapi.Run{Routine: r.Name, ID: id, Folder: folder, Plugins: r.Plugins}
}

func (h *host) DataDir() (string, error) {
	if err := os.MkdirAll(h.dataDir, 0o700); err != nil {
		return "", err
	}
	return h.dataDir, nil
}

func (h *host) Run() pluginapi.Run {
	run := h.run
	run.Plugins = nil // each plugin reads its own section (RoutineConfig)
	return run
}

func (h *host) RoutineConfig(v any) error {
	section, ok := h.run.Plugins[h.plugin.Name]
	if !ok {
		return nil
	}
	if err := pluginapi.DecodeSection(section, v); err != nil {
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

func (h *host) Config(v any) error { return h.cfg.DecodePlugin(h.plugin.Name, v) }

// Secret returns a secret the plugin declared; undeclared names are refused so a
// plugin can only read its own secrets.
func (h *host) Secret(name string) (string, error) {
	for _, s := range h.plugin.Secrets {
		if s.Name == name {
			v, err := h.conn.Secrets.Get(pluginapi.SecretKey(h.plugin.Name, name))
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
