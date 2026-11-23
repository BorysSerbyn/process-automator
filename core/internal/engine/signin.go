package engine

import (
	"errors"
	"sort"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/config"
	"github.com/BorysSerbyn/process-automator/core/internal/hooks"
	"github.com/BorysSerbyn/process-automator/core/internal/programs"
	"github.com/BorysSerbyn/process-automator/core/internal/secrets"
	"github.com/BorysSerbyn/process-automator/core/internal/watchers"
)

// SignIn is one name `auth login` takes and what signing in to it is: the
// secrets it asks for, in order, then (for a remote server) the OAuth
// sign-in in the browser. auth login, logout and status, setup and
// readiness all read this one list.
type SignIn struct {
	Name string
	// Kind is what it is, as `auth status` shows it: remote or local (a
	// configured server), in-process (a plugin), api-key or token (a model
	// backend's), program, hook or watcher (their own secrets).
	Kind    string
	Secrets []Secret
	// OAuth is a remote server's browser sign-in, after Secrets (a
	// pre-registered app's client credentials, when it has one).
	OAuth bool
	// Pairing is a local server's hint for approving the app, shown while
	// connecting to it before its secrets are asked for.
	Pairing string
	// Prepare is what I do outside the app first (its plugin's Prepare).
	Prepare string
	// Optional is a backend's secret it works without: listed by `auth
	// status` only when stored (or unreadable).
	Optional bool
	// AccountOf is, for an account, the plugin it's an account of.
	AccountOf string
}

// Secret is one secret a sign-in asks for.
type Secret struct {
	Key, Label string
	// Former are keys it was stored under before Key: still read, and
	// forgotten with it.
	Former []string
	// Kept is a pre-registered OAuth app's client credential: asked for only
	// while it isn't stored, and kept on logout.
	Kept bool
}

// SignIns is every name `auth login` takes, by name: the model backends'
// keys, the configured servers, the in-process plugins that declare secrets,
// and the programs that do.
func (a *App) SignIns() []SignIn {
	out := []SignIn{
		{Name: "anthropic", Kind: "api-key", Secrets: []Secret{{Key: AnthropicKey, Label: "your Anthropic API key"}}},
		{Name: "claude-code", Kind: "token", Optional: true,
			Secrets: []Secret{{Key: ClaudeCodeTokenKey, Label: "the token printed by `claude setup-token`"}}},
		{Name: agent.LocalProvider, Kind: "api-key", Optional: true,
			Secrets: []Secret{{Key: LocalKey, Former: []string{formerLocalKey}, Label: "the local model server's API key"}}},
	}
	for name, srv := range a.Config.MCPServers {
		s := SignIn{Name: name, Kind: "local", OAuth: config.OAuth(srv), Pairing: srv.Pairing}
		if config.Remote(srv) {
			s.Kind = "remote"
		}
		if oc := srv.OAuthClient; oc != nil && s.OAuth {
			s.Secrets = append(s.Secrets, Secret{Key: oc.ClientIDKey, Label: "the app's Client ID", Kept: true})
			if oc.ClientSecretKey != "" {
				s.Secrets = append(s.Secrets, Secret{Key: oc.ClientSecretKey, Label: "the app's Client Secret", Kept: true})
			}
		}
		for _, k := range config.SecretKeys(srv) {
			s.Secrets = append(s.Secrets, Secret{Key: k, Label: config.AccountKeyLabel(name, k)})
		}
		out = append(out, s)
	}
	for _, p := range a.Plugins {
		if p.InProcess == nil || len(p.InProcess.Secrets) == 0 {
			continue
		}
		s := SignIn{Name: p.Name, Kind: "in-process"}
		for _, sec := range p.InProcess.Secrets {
			s.Secrets = append(s.Secrets, Secret{Key: PluginSecretKey(p.Name, sec.Name), Label: sec.Prompt})
		}
		out = append(out, s)
	}
	if set, err := programs.Load(a.Layout.Programs()); err == nil {
		for _, p := range set.Programs {
			if len(p.Secrets) == 0 {
				continue
			}
			s := SignIn{Name: p.Name, Kind: "program"}
			for _, sec := range p.Secrets {
				s.Secrets = append(s.Secrets, (&program{p: p}).secret(sec))
			}
			out = append(out, s)
		}
	}
	if set, err := hooks.Load(a.Layout.Hooks()); err == nil {
		for _, h := range set.Hooks {
			if h.Program == nil || len(h.Program.Secrets) == 0 {
				continue
			}
			s := SignIn{Name: h.Name, Kind: "hook"}
			for _, sec := range h.Program.Secrets {
				s.Secrets = append(s.Secrets, (&program{p: *h.Program}).secret(sec))
			}
			out = append(out, s)
		}
	}
	if set, err := watchers.Load(a.Layout.Watchers()); err == nil {
		for _, w := range set.Watchers {
			if len(w.Program.Secrets) == 0 {
				continue
			}
			s := SignIn{Name: w.Name, Kind: "watcher"}
			for _, sec := range w.Program.Secrets {
				s.Secrets = append(s.Secrets, watcherSecret(w, sec))
			}
			out = append(out, s)
		}
	}
	for i := range out {
		if p, ok := a.FindPlugin(out[i].Name); ok {
			out[i].Prepare = p.Prepare
		}
		out[i].AccountOf = a.Accounts[out[i].Name]
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// FindSignIn is the sign-in a name names (the local server's former name
// too).
func (a *App) FindSignIn(name string) (SignIn, bool) {
	if name == agent.FormerLocalProvider {
		name = agent.LocalProvider
	}
	for _, s := range a.SignIns() {
		if s.Name == name {
			return s, true
		}
	}
	return SignIn{}, false
}

// Stored is the secret's value and whether it's stored, under its key or
// a former one; err is a secret that can't be read (a locked keychain),
// which is never taken for one that isn't stored.
func (a *App) Stored(s Secret) (value string, stored bool, err error) {
	for _, k := range append([]string{s.Key}, s.Former...) {
		if v, ok, err := secrets.Lookup(a.Connector.Secrets, k); err != nil || ok {
			return v, ok, err
		}
	}
	return "", false, nil
}

// SignInState is whether a sign-in has what it needs stored.
type SignInState struct {
	SignedIn bool
	// Problem says why it can't be told: a secret that can't be read.
	Problem string
}

// State is where a sign-in stands: every secret stored and, for OAuth, its
// tokens.
func (a *App) State(s SignIn) SignInState {
	st := SignInState{SignedIn: true}
	if s.OAuth {
		in, err := a.Connector.SignedIn(s.Name)
		if err != nil {
			return SignInState{Problem: "its sign-in can't be read: " + err.Error()}
		}
		st.SignedIn = in
	}
	for _, sec := range s.Secrets {
		switch _, stored, err := a.Stored(sec); {
		case err != nil:
			return SignInState{Problem: "a secret can't be read: " + err.Error()}
		case !stored:
			st.SignedIn = false
		}
	}
	return st
}

// Listed is whether `auth status` lists the sign-in: an optional one only
// when one of its secrets is stored, or can't be read.
func (a *App) Listed(s SignIn) bool {
	if !s.Optional {
		return true
	}
	for _, sec := range s.Secrets {
		if _, stored, err := a.Stored(sec); stored || err != nil {
			return true
		}
	}
	return false
}

// Missing is whether something a sign-in asks for still is: a secret that
// isn't stored (one that can't be read isn't missing) or, with no secrets
// to ask for, the OAuth sign-in.
func (a *App) Missing(s SignIn) bool {
	if len(s.Secrets) == 0 {
		in, err := a.Connector.SignedIn(s.Name)
		return s.OAuth && err == nil && !in
	}
	for _, sec := range s.Secrets {
		if _, stored, err := a.Stored(sec); err == nil && !stored {
			return true
		}
	}
	return false
}

// SignedIn is, for a server or plugin that signs in, whether it is (false
// when a secret can't be read); ok is false for one that doesn't.
func (a *App) SignedIn(name string) (in, ok bool) {
	s, found := a.FindSignIn(name)
	if !found || (len(s.Secrets) == 0 && !s.OAuth) {
		return false, false
	}
	return a.State(s).SignedIn, true
}

// Logout forgets a sign-in's credentials: its OAuth tokens and its
// secrets, except a pre-registered app's client credentials and keys
// another sign-in references (external servers may share one).
func (a *App) Logout(s SignIn) error { return a.forget(s, false) }

// forget is Logout, and with all a pre-registered app's client credentials
// too (an account's, which are its own).
func (a *App) forget(s SignIn, all bool) error {
	if s.OAuth {
		if err := a.Connector.Logout(s.Name); err != nil {
			return err
		}
	}
	shared := map[string]bool{}
	for _, other := range a.SignIns() {
		if other.Name == s.Name {
			continue
		}
		for _, sec := range other.Secrets {
			shared[sec.Key] = true
		}
	}
	for _, sec := range s.Secrets {
		if (sec.Kept && !all) || shared[sec.Key] {
			continue
		}
		for _, k := range append([]string{sec.Key}, sec.Former...) {
			if err := a.Connector.Secrets.Delete(k); err != nil && !errors.Is(err, secrets.ErrNotFound) {
				return err
			}
		}
	}
	return nil
}
