package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/config"
	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// PluginInfo is one row of `plugins list --json`.
type PluginInfo struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"` // in-process, remote, local
	Description string `json:"description,omitempty"`
	Endpoint    string `json:"endpoint,omitempty"` // URL or command, for external plugins
}

func newPluginsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "plugins", Short: "Plugins (MCP servers) routines can use"}
	list := &cobra.Command{
		Use:         "list",
		Annotations: readOnly(),
		Short:       "List installed plugins: the built-in ones plus servers added in config",
		Args:        cobra.NoArgs,
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, _ []string) error {
			var rows []PluginInfo
			seen := map[string]bool{}
			for _, p := range a.Plugins {
				seen[p.Name] = true
				row := PluginInfo{Name: p.Name, Description: p.Description, Kind: "in-process"}
				if p.External != nil {
					row.Kind, row.Endpoint = endpoint(a.Config.MCPServers[p.Name])
				}
				rows = append(rows, row)
			}
			for name := range a.Config.MCPServers {
				if !seen[name] {
					kind, ep := endpoint(a.Config.MCPServers[name])
					desc := a.Config.MCPServers[name].Description
					if desc == "" {
						desc = "(from config)"
					}
					rows = append(rows, PluginInfo{Name: name, Kind: kind, Endpoint: ep, Description: desc})
				}
			}
			if wantsJSON(cmd) {
				return writeJSON(cmd.OutOrStdout(), rows)
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tKIND\tDESCRIPTION")
			for _, r := range rows {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Name, r.Kind, r.Description)
			}
			return tw.Flush()
		}),
	}
	jsonFlag(list.Flags(), []PluginInfo{})
	cmd.AddCommand(list, newPluginsAddCmd(), newPluginsRemoveCmd(), newPluginsCheckCmd())
	return cmd
}

// PluginChange is the output of `plugins add/remove --json`.
type PluginChange struct {
	Plugin string `json:"plugin"`
	// Kind and Endpoint describe the server after the change (absent when removed).
	Kind     string `json:"kind,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	// SignIn is the command that signs in to it, when it needs credentials.
	SignIn string `json:"signIn,omitempty"`
	// Trial is what trying it found (adds only).
	Trial *PluginTrial `json:"trial,omitempty"`
}

func newPluginsAddCmd() *cobra.Command {
	var spec pluginapi.ServerSpec
	var env, headers []string
	var dir string
	var replace bool
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add an MCP server routines can use, after trying it (this starts the server)",
		Long: "Adds a remote (--url) or local (--command) MCP server under <name>, or installs a plugin folder " +
			"(--dir: an MCP server with a plugin.yaml giving its command, args, env and description; the folder is " +
			"copied into the app). First it tries the server: starts it, does the MCP handshake and checks its " +
			"tools; if that fails nothing is added. This runs the server's code. A plugin folder's tools must all " +
			"declare annotations (readOnlyHint). A server that needs credentials is tried as far as it can be; " +
			"sign in with `auth login <name>`, then `plugins check <name>`. Secrets are referenced as {key} in " +
			"--header/--env values (or the manifest's env), never given here. Routines use it by listing <name> " +
			"in automator.servers. How to add one, or write your own: `" + product.Command + " guide plugins`.",
		Example: "  " + product.Command + " plugins add notion --url https://mcp.notion.com/mcp\n" +
			"  " + product.Command + " plugins add weather --dir ./weather-mcp\n" +
			"  " + product.Command + " plugins add acme --url https://mcp.acme.dev --auth headers --header 'Authorization=Bearer {acme-key}'\n" +
			"  " + product.Command + " plugins add files --command npx --arg -y --arg @modelcontextprotocol/server-filesystem --arg ~/notes",
		Args: cobra.ExactArgs(1),
		RunE: withAppForEdit(func(cmd *cobra.Command, a *engine.App, args []string) error {
			var err error
			name := args[0]
			if !config.ValidName(name) {
				return fmt.Errorf("%q can't name a plugin: lowercase letters, digits and dashes", name)
			}
			if _, ok := a.InProcess(name); ok {
				return fmt.Errorf("%q is a built-in plugin; choose another name", name)
			}
			prev, exists := a.Config.MCPServers[name]
			if exists && !replace {
				return fmt.Errorf("%q already exists; use --replace to replace it", name)
			}
			if c := spec.Command; c != "" && strings.ContainsRune(c, filepath.Separator) && !filepath.IsAbs(c) {
				return fmt.Errorf("--command %q: use an absolute path (it runs from the scheduler, not from here)", c)
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Trying %s: starting it, MCP handshake, listing its tools…\n", name)
			var trial PluginTrial
			finish := func(bool) {}
			if dir != "" {
				if spec.URL != "" || spec.Command != "" || len(spec.Args)+len(env)+len(headers) > 0 {
					return errors.New("--dir takes everything from the folder's plugin.yaml; don't combine it with --url, --command, --arg, --env or --header")
				}
				if spec, trial, finish, err = installFolder(cmd.Context(), a, name, dir); err != nil {
					return err
				}
			} else {
				if spec.Env, err = keyValues("--env", env); err != nil {
					return err
				}
				if spec.Headers, err = keyValues("--header", headers); err != nil {
					return err
				}
				if err := config.ValidateServer(name, spec); err != nil {
					return err
				}
				if trial, err = tryPlugin(cmd.Context(), a, name, &spec, false); err != nil {
					return fmt.Errorf("trial failed, nothing added: %w", err)
				}
			}
			if err := config.SetServer(a.Layout.Config(), name, spec); err != nil {
				finish(false)
				return err
			}
			finish(true)
			// A replaced plugin folder the new definition no longer uses.
			if exists && managed(a, prev) && filepath.Clean(prev.Dir) != filepath.Clean(spec.Dir) {
				_ = os.RemoveAll(prev.Dir)
			}
			kind, ep := endpoint(spec)
			out := PluginChange{Plugin: name, Kind: kind, Endpoint: ep, Trial: &trial}
			if trial.NeedsSignIn {
				out.SignIn = product.Command + " auth login " + name
			}
			if wantsJSON(cmd) {
				return writeJSON(cmd.OutOrStdout(), out)
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Added %s (%s %s).\n", name, kind, ep)
			if trial.NeedsSignIn {
				fmt.Fprintf(w, "It needs credentials before it can be tried fully.\nSign in: %s\nThen: "+product.Command+" plugins check %s\n", out.SignIn, name)
			} else {
				fmt.Fprintf(w, "Tried it: %s\n", toolSummary(trial.Tools))
				for _, t := range trial.Tools {
					kind := "write"
					if t.ReadOnly {
						kind = "read-only"
					}
					fmt.Fprintf(w, "  %-32s %s\n", t.Name, kind)
				}
			}
			for _, warn := range trial.Warnings {
				fmt.Fprintf(w, "warning: %s\n", warn)
			}
			return nil
		}),
	}
	f := cmd.Flags()
	f.StringVar(&spec.URL, "url", "", "a remote server's URL")
	f.StringVar(&spec.Command, "command", "", "a local server's command (stdio)")
	f.StringArrayVar(&spec.Args, "arg", nil, "an argument for --command (repeatable)")
	f.StringArrayVar(&env, "env", nil, "KEY=value for --command's environment; values may reference {secret} (repeatable)")
	f.StringVar(&spec.Auth, "auth", "", "remote sign-in: oauth (default), headers or none")
	f.StringVar(&spec.Transport, "transport", "", "remote transport: streamable (default) or sse")
	f.StringArrayVar(&headers, "header", nil, "Name=value sent to a remote server; values may reference {secret} (repeatable)")
	f.StringArrayVar(&spec.Scopes, "scope", nil, "an OAuth scope to request (repeatable)")
	f.StringVar(&dir, "dir", "", "install a plugin folder (an MCP server with a plugin.yaml); it's copied into the app")
	f.StringVar(&spec.Description, "description", "", "what the server is for (shown by plugins list)")
	_ = f.SetAnnotation("env", annRedact, []string{"true"})
	_ = f.SetAnnotation("header", annRedact, []string{"true"})
	f.BoolVar(&replace, "replace", false, "replace a server of the same name (a built-in preset is overridden)")
	jsonFlag(f, PluginChange{})
	return cmd
}

func keyValues(flag string, kvs []string) (map[string]string, error) {
	if len(kvs) == 0 {
		return nil, nil
	}
	m := map[string]string{}
	for _, kv := range kvs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("%s %q: want name=value", flag, kv)
		}
		m[k] = v
	}
	return m, nil
}

func newPluginsRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove an MCP server added in config, and forget its credentials",
		Long: "Removes mcpServers.<name> from config, deletes its folder if it was installed with --dir, and " +
			"forgets its stored credentials. For a built-in preset that " +
			"config overrode, the preset applies again (its credentials are kept). Built-in plugins can't be removed.",
		Args: cobra.ExactArgs(1),
		RunE: withAppForEdit(func(cmd *cobra.Command, a *engine.App, args []string) error {
			name := args[0]
			p, bundled := a.FindPlugin(name)
			if bundled && p.InProcess != nil {
				return fmt.Errorf("%q is a built-in plugin; it can't be removed", name)
			}
			spec := a.Config.MCPServers[name]
			removed, err := config.RemoveServer(a.Layout.Config(), name)
			if err != nil {
				return err
			}
			// Only once it's out of config: credentials and its folder.
			if removed && !bundled {
				if err := logout(a, name); err != nil {
					return fmt.Errorf("removed %s, but couldn't forget its credentials: %w", name, err)
				}
			}
			if removed && managed(a, spec) {
				if err := os.RemoveAll(spec.Dir); err != nil {
					return fmt.Errorf("removed %s, but not its folder: %w", name, err)
				}
			}
			if !removed {
				if bundled {
					return fmt.Errorf("%q is a built-in preset, not added in config; it can't be removed", name)
				}
				return fmt.Errorf("no plugin %q in config", name)
			}
			out := PluginChange{Plugin: name}
			msg := fmt.Sprintf("Removed %s.", name)
			if bundled && p.External != nil {
				out.Kind, out.Endpoint = endpoint(*p.External)
				msg = fmt.Sprintf("Removed the config override of %s; the built-in preset applies again.", name)
			}
			return emit(cmd, out, msg)
		}),
	}
	jsonFlag(cmd.Flags(), PluginChange{})
	return cmd
}

// endpoint is a server's kind (remote or local) and where it is (its URL
// or command).
func endpoint(s pluginapi.ServerSpec) (kind, where string) {
	if config.Remote(s) {
		return "remote", s.URL
	}
	return "local", s.Command
}
