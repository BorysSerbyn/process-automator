package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/secrets"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "auth", Short: "Manage sign-in to MCP servers"}
	cmd.AddCommand(newAuthLoginCmd(), newAuthLogoutCmd(), newAuthStatusCmd())
	return cmd
}

func newAuthLoginCmd() *cobra.Command {
	var browser bool
	cmd := &cobra.Command{
		Use: "login <server|anthropic|claude-code|local>",
		Short: "Sign in to a server (browser for remote servers; paste credentials for local ones; " +
			"`anthropic` stores the API key; `claude-code` stores a `claude setup-token` token, optional; " +
			"`local` stores the local model server's API key, for one that needs it (experimental, untested); " +
			"codex keeps its own sign-in: `codex login`)",
		Long: "Signs in to a server. OAuth servers open the browser. Keys and tokens are pasted into the terminal " +
			"(or read from stdin), or with --browser entered on a local page that stores them straight in the " +
			"system's secret store (for frontends without a terminal).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := loadApp(cmd)
			if err != nil {
				return err
			}
			ui := &terminalUI{in: bufio.NewReader(cmd.InOrStdin()), out: cmd.OutOrStdout(), browser: browser}
			return login(cmd.Context(), a, args[0], ui)
		},
	}
	cmd.Flags().BoolVar(&browser, "browser", false, "enter keys and tokens on a local page in the browser instead of the terminal")
	return cmd
}

// loginUI is how a sign-in reaches me: a terminal, or an MCP client. Secrets
// go from me to the keychain, never through anything else (e.g. a model).
type loginUI interface {
	// Prepare shows what I do outside the app before signing in, and waits
	// for me to say it's done.
	Prepare(ctx context.Context, server, steps string) error
	// Secret asks me for one secret value.
	Secret(ctx context.Context, key, label string) (string, error)
	// SignIn sends me to an OAuth sign-in page.
	SignIn(ctx context.Context, server, url string) error
	// Say reports progress.
	Say(format string, args ...any)
}

// localName is the sign-in name for the local server: openai-compat is its
// former name, still taken.
func localName(name string) string {
	if name == agent.FormerLocalProvider {
		return agent.LocalProvider
	}
	return name
}

// login signs in to a server, plugin, or the model engine.Backends.
func login(ctx context.Context, a *engine.App, name string, ui loginUI) error {
	ask := func(key, label string) error {
		if label == "" {
			label = key
		}
		v, err := ui.Secret(ctx, key, label)
		if err != nil {
			return err
		}
		if v = strings.TrimSpace(v); v == "" {
			return fmt.Errorf("%s is required", key)
		}
		return a.Connector.Secrets.Set(key, v)
	}
	switch name = localName(name); name {
	case "anthropic":
		if err := ask(engine.AnthropicKey, "your Anthropic API key"); err != nil {
			return err
		}
		ui.Say("Saved the Anthropic API key.")
		return nil
	case "claude-code":
		if err := ask(engine.ClaudeCodeTokenKey, "the token printed by `claude setup-token`"); err != nil {
			return err
		}
		ui.Say("Saved the Claude Code token.")
		return nil
	case "local":
		if err := ask(engine.LocalKey, "the local model server's API key"); err != nil {
			return err
		}
		ui.Say("Saved the local model server's API key.")
		return nil
	case "codex":
		return errors.New("the codex backend uses Codex's own sign-in: run `codex login` in a terminal")
	}
	// What I do outside the app first, before any credential is asked for
	// (only while something's still missing).
	prepare := func(missing bool) error {
		if p, ok := a.FindPlugin(name); ok && p.Prepare != "" && missing {
			return ui.Prepare(ctx, name, p.Prepare)
		}
		return nil
	}
	missing := func(keys ...string) bool {
		for _, k := range keys {
			// Only a secret that isn't stored is missing: one that can't be
			// read (locked) is asked for again by nothing.
			if _, stored, err := secrets.Lookup(a.Connector.Secrets, k); k != "" && err == nil && !stored {
				return true
			}
		}
		return false
	}
	if p, ok := a.FindPlugin(name); ok && p.Serve != nil {
		if len(p.Secrets) == 0 {
			return fmt.Errorf("%s needs no sign-in", name)
		}
		var keys []string
		for _, s := range p.Secrets {
			keys = append(keys, pluginapi.SecretKey(name, s.Name))
		}
		if err := prepare(missing(keys...)); err != nil {
			return err
		}
		for _, s := range p.Secrets {
			if err := ask(pluginapi.SecretKey(name, s.Name), s.Prompt); err != nil {
				return err
			}
		}
		ui.Say("Saved credentials for %s.", name)
		return nil
	}
	srv, ok := a.Config.MCPServers[name]
	if !ok {
		return fmt.Errorf("unknown plugin %q (sign-in names: %s)", name, strings.Join(signInNames(a), ", "))
	}
	if srv.AuthMode() == pluginapi.AuthOAuth && srv.Remote() {
		// A pre-registered app's client credentials are asked for the first time.
		if oc := srv.OAuthClient; oc != nil {
			if err := prepare(missing(oc.ClientIDKey, oc.ClientSecretKey)); err != nil {
				return err
			}
			for _, k := range []struct{ key, label string }{
				{oc.ClientIDKey, "the app's Client ID"}, {oc.ClientSecretKey, "the app's Client Secret"},
			} {
				if k.key == "" || !missing(k.key) {
					continue
				}
				if err := ask(k.key, k.label); err != nil {
					return err
				}
			}
		} else if err := prepare(!signedIn(a)[name]); err != nil {
			return err
		}
		if err := a.Connector.Login(ctx, name, func(url string) error { return ui.SignIn(ctx, name, url) }); err != nil {
			return err
		}
		ui.Say("Signed in to %s.", name)
		return nil
	}
	keys := srv.SecretKeys()
	if len(keys) == 0 {
		return fmt.Errorf("%s needs no sign-in", name)
	}
	if err := prepare(missing(keys...)); err != nil {
		return err
	}
	if srv.Pairing != "" {
		ui.Say("Connecting to %s so it can ask you to approve "+product.Command+"…\n%s", name, srv.Pairing)
		if err := a.Connector.Pair(ctx, name); err != nil {
			ui.Say("(connection ended: %v)\nIf you already copied the credential, enter it next.", err)
		}
	}
	for _, key := range keys {
		if err := ask(key, key); err != nil {
			return err
		}
	}
	ui.Say("Saved credentials for %s.", name)
	return nil
}

// terminalUI signs in from a terminal: secrets are pasted (or piped) in, or
// entered on a local browser page with --browser.
type terminalUI struct {
	in      *bufio.Reader
	out     io.Writer
	browser bool
	steps   string // what to do first, also shown on the secret page
}

func (t *terminalUI) Prepare(_ context.Context, server, steps string) error {
	t.steps = steps
	t.Say("Before signing in to %s:\n%s", server, plainSteps(steps))
	if t.browser {
		return nil // the secret page shows them too
	}
	fmt.Fprint(t.out, "\nDone? [y/N] ")
	line, _ := t.in.ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
		return fmt.Errorf("sign in to %s once that's done", server)
	}
	return nil
}

func (t *terminalUI) Secret(ctx context.Context, _, label string) (string, error) {
	if t.browser {
		return secretPage(ctx, label, t.steps, func(url string) (func(), error) {
			t.Say("Enter %s in your browser:\n  %s", label, url)
			if err := openExternal(url); err != nil {
				t.Say("(open that link yourself)")
			}
			return func() {}, nil
		})
	}
	fmt.Fprintf(t.out, "Paste %s and press Enter: ", label)
	line, err := t.in.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return line, nil
}

func (t *terminalUI) SignIn(_ context.Context, server, url string) error {
	t.Say("Sign in to %s in your browser:\n  %s", server, url)
	if err := openExternal(url); err != nil {
		t.Say("(open that link yourself)")
	}
	return nil
}

func (t *terminalUI) Say(format string, args ...any) { fmt.Fprintf(t.out, format+"\n", args...) }

func newAuthLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout <server|anthropic|claude-code|local>",
		Short: "Forget a server's stored credentials",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := loadApp(cmd)
			if err != nil {
				return err
			}
			return logout(a, args[0])
		},
	}
}

// logout forgets a server's or plugin's stored credentials.
func logout(a *engine.App, name string) error {
	switch name = localName(name); name {
	case "anthropic":
		return a.Connector.Secrets.Delete(engine.AnthropicKey)
	case "claude-code":
		return a.Connector.Secrets.Delete(engine.ClaudeCodeTokenKey)
	case "local":
		for _, k := range engine.LocalKeys {
			if err := a.Connector.Secrets.Delete(k); err != nil {
				return err
			}
		}
		return nil
	}
	if p, ok := a.FindPlugin(name); ok && p.Serve != nil {
		for _, s := range p.Secrets {
			if err := a.Connector.Secrets.Delete(pluginapi.SecretKey(p.Name, s.Name)); err != nil {
				return err
			}
		}
		return nil
	}
	srv, ok := a.Config.MCPServers[name]
	if !ok {
		return fmt.Errorf("unknown server %q", name)
	}
	if srv.AuthMode() == pluginapi.AuthOAuth {
		if err := a.Connector.Logout(name); err != nil {
			return err
		}
	}
	// Secrets are referenced by key, so other servers may share one: those stay.
	shared := secretKeysUsedBy(a, name)
	for _, key := range srv.SecretKeys() {
		if shared[key] {
			continue
		}
		if err := a.Connector.Secrets.Delete(key); err != nil && !errors.Is(err, secrets.ErrNotFound) {
			return err
		}
	}
	return nil
}

// secretKeysUsedBy is every secret key referenced by configured servers other
// than except (headers, env and OAuth client credentials).
func secretKeysUsedBy(a *engine.App, except string) map[string]bool {
	used := map[string]bool{}
	for name, s := range a.Config.MCPServers {
		if name == except {
			continue
		}
		for _, k := range s.SecretKeys() {
			used[k] = true
		}
		if oc := s.OAuthClient; oc != nil {
			used[oc.ClientIDKey], used[oc.ClientSecretKey] = true, true
		}
	}
	return used
}

// AuthStatus is one row of `auth status --json`.
type AuthStatus struct {
	Server   string `json:"server"`
	Kind     string `json:"kind"`
	SignedIn bool   `json:"signedIn"`
	// Problem says why it can't be told: a secret that can't be read (a
	// locked Keychain or keyring), which isn't the same as not signed in.
	Problem string `json:"problem,omitempty"`
}

func newAuthStatusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:         "status",
		Annotations: readOnly(),
		Short:       "Show which servers have credentials stored",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := loadApp(cmd)
			if err != nil {
				return err
			}
			rows, err := authStatuses(a)
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), rows)
			}
			for _, r := range rows {
				state := "signed in"
				if !r.SignedIn {
					state = "NOT signed in (" + product.Command + " auth login " + r.Server + ")"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%-10s %-7s %s\n", r.Server, r.Kind, state)
			}
			return nil
		},
	}
	jsonFlag(cmd.Flags(), &asJSON, []AuthStatus{})
	return cmd
}

// authStatuses is whether each server, plugin and backend that can sign in has
// what it needs stored, sorted by name.
func authStatuses(a *engine.App) ([]AuthStatus, error) {
	var rows []AuthStatus
	for name, srv := range a.Config.MCPServers {
		row := AuthStatus{Server: name, Kind: "local", SignedIn: true}
		if srv.Remote() {
			row.Kind = "remote"
		}
		if srv.AuthMode() == pluginapi.AuthOAuth {
			signedIn, err := a.Connector.SignedIn(name)
			if err != nil {
				return nil, err
			}
			row.SignedIn = signedIn
		}
		for _, key := range srv.SecretKeys() {
			row.check(a, key)
		}
		rows = append(rows, row)
	}
	for _, p := range a.Plugins {
		if p.Serve == nil || len(p.Secrets) == 0 {
			continue
		}
		row := AuthStatus{Server: p.Name, Kind: "in-process", SignedIn: true}
		for _, s := range p.Secrets {
			row.check(a, pluginapi.SecretKey(p.Name, s.Name))
		}
		rows = append(rows, row)
	}
	anthropic := AuthStatus{Server: "anthropic", Kind: "api-key", SignedIn: true}
	anthropic.check(a, engine.AnthropicKey)
	rows = append(rows, anthropic)
	// The token is optional (without it Claude Code uses its own login), and so
	// is this key (local servers need none): listed when stored, or unreadable.
	optional := []struct {
		row  AuthStatus
		keys []string
	}{
		{AuthStatus{Server: "claude-code", Kind: "token"}, []string{engine.ClaudeCodeTokenKey}},
		{AuthStatus{Server: "local", Kind: "api-key"}, engine.LocalKeys}, // under its former key too
	}
	for _, o := range optional {
		for _, key := range o.keys {
			if _, stored, err := secrets.Lookup(a.Connector.Secrets, key); stored || err != nil {
				o.row.SignedIn = true
				o.row.check(a, key)
				rows = append(rows, o.row)
				break
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Server < rows[j].Server })
	return rows, nil
}

// check marks the row signed out when key isn't stored, and with a problem
// when it can't be read.
func (r *AuthStatus) check(a *engine.App, key string) {
	switch _, stored, err := secrets.Lookup(a.Connector.Secrets, key); {
	case err != nil:
		r.SignedIn, r.Problem = false, "a secret can't be read: "+err.Error()
	case !stored:
		r.SignedIn = false
	}
}

// signInNames is everything `auth login` accepts.
func signInNames(a *engine.App) []string {
	names := []string{"anthropic", "claude-code", "local"}
	for name := range a.Config.MCPServers {
		names = append(names, name)
	}
	for _, p := range a.Plugins {
		if p.Serve != nil && len(p.Secrets) > 0 {
			names = append(names, p.Name)
		}
	}
	sort.Strings(names)
	return names
}
