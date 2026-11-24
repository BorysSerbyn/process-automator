package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
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
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, args []string) error {
			ui := &terminalUI{prompt: newPrompt(cmd), browser: browser}
			return login(cmd.Context(), a, args[0], ui)
		}),
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

// login signs in to a server, a plugin, or a model backend's key: what's
// still to do outside the app first, a local server's pairing, each secret
// it asks for, then a remote server's OAuth sign-in.
func login(ctx context.Context, a *engine.App, name string, ui loginUI) error {
	if name == "codex" {
		return errors.New("the codex backend uses Codex's own sign-in: run `" + agent.NewestCodex().LoginCommand() + "` in a terminal")
	}
	s, ok := a.FindSignIn(name)
	if !ok {
		if _, plugin := a.FindPlugin(name); plugin {
			return fmt.Errorf("%s needs no sign-in", name)
		}
		return fmt.Errorf("unknown plugin %q (sign-in names: %s)", name, strings.Join(signInNames(a), ", "))
	}
	if len(s.Secrets) == 0 && !s.OAuth {
		return fmt.Errorf("%s needs no sign-in", s.Name)
	}
	// What I do outside the app first, before any credential is asked for
	// (only while something's still missing).
	if s.Prepare != "" && a.Missing(s) {
		if err := ui.Prepare(ctx, s.Name, s.Prepare); err != nil {
			return err
		}
	}
	if s.Pairing != "" {
		ui.Say("Connecting to %s so it can ask you to approve "+product.Command+"…\n%s", s.Name, s.Pairing)
		if err := a.Connector.Pair(ctx, s.Name); err != nil {
			ui.Say("(connection ended: %v)\nIf you already copied the credential, enter it next.", err)
		}
	}
	for _, sec := range s.Secrets {
		if _, stored, err := a.Stored(sec); sec.Kept && (stored || err != nil) {
			continue // a pre-registered app's client credentials, asked for once
		}
		v, err := ui.Secret(ctx, sec.Key, sec.Label)
		if err != nil {
			return err
		}
		if v = strings.TrimSpace(v); v == "" {
			return fmt.Errorf("%s is required", sec.Key)
		}
		if err := a.Connector.Secrets.Set(sec.Key, v); err != nil {
			return err
		}
	}
	if s.OAuth {
		if err := a.Connector.Login(ctx, s.Name, func(url string) error { return ui.SignIn(ctx, s.Name, url) }); err != nil {
			return err
		}
		ui.Say("Signed in to %s.", s.Name)
		return nil
	}
	ui.Say("Saved credentials for %s.", s.Name)
	return nil
}

// terminalUI signs in from a terminal: secrets are pasted (or piped) in, or
// entered on a local browser page with --browser.
type terminalUI struct {
	*prompt
	browser bool
	steps   string // what to do first, also shown on the secret page
}

func (t *terminalUI) Prepare(_ context.Context, server, steps string) error {
	t.steps = steps
	t.Say("Before signing in to %s:\n%s", server, plainSteps(steps))
	if t.browser {
		return nil // the secret page shows them too
	}
	if !t.Confirm("\nDone?", false) {
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
		RunE: withApp(func(_ *cobra.Command, a *engine.App, args []string) error {
			return logout(a, args[0])
		}),
	}
}

// logout forgets a server's, plugin's or backend's stored credentials.
func logout(a *engine.App, name string) error {
	s, ok := a.FindSignIn(name)
	if !ok {
		return fmt.Errorf("unknown server %q", name)
	}
	return a.Logout(s)
}

// AuthStatus is one row of `auth status --json`.
type AuthStatus struct {
	Server   string `json:"server"`
	Kind     string `json:"kind"`
	SignedIn bool   `json:"signedIn"`
	// Problem says why it can't be told: a secret that can't be read (a
	// locked Keychain or keyring), which isn't the same as not signed in.
	Problem string `json:"problem,omitempty"`
	// AccountOf is, for an account, the plugin it's an account of.
	AccountOf string `json:"accountOf,omitempty"`
}

func newAuthStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "status",
		Annotations: readOnly(),
		Short:       "Show which servers have credentials stored",
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, _ []string) error {
			rows := authStatuses(a)
			if wantsJSON(cmd) {
				return writeJSON(cmd.OutOrStdout(), rows)
			}
			for _, r := range rows {
				state := "signed in"
				if !r.SignedIn {
					state = "NOT signed in (" + product.Command + " auth login " + r.Server + ")"
				}
				if r.AccountOf != "" {
					state += " (an account of " + r.AccountOf + ")"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%-10s %-7s %s\n", r.Server, r.Kind, state)
			}
			return nil
		}),
	}
	jsonFlag(cmd.Flags(), []AuthStatus{})
	return cmd
}

// authStatuses is whether each server, plugin and backend that can sign in has
// what it needs stored, sorted by name.
func authStatuses(a *engine.App) []AuthStatus {
	var rows []AuthStatus
	for _, s := range a.SignIns() {
		if a.Listed(s) {
			st := a.State(s)
			rows = append(rows, AuthStatus{Server: s.Name, Kind: s.Kind, SignedIn: st.SignedIn, Problem: st.Problem, AccountOf: s.AccountOf})
		}
	}
	return rows
}

// signInNames is everything `auth login` takes.
func signInNames(a *engine.App) []string {
	var names []string
	for _, s := range a.SignIns() {
		names = append(names, s.Name)
	}
	return names
}
