package engine

import (
	"errors"
	"fmt"

	"github.com/BorysSerbyn/process-automator/core/internal/config"
)

// AddAccount adds name as an account of the plugin (or added server) of: the
// same server under a name of its own, signed in with its own credentials
// (`auth login <name>`). The plugin must sign in to something (an account
// of one that doesn't would be the same server again), and can't be an
// account itself.
func (a *App) AddAccount(name, of string) error {
	if !config.ValidName(name) {
		return fmt.Errorf("%q can't name an account: lowercase letters, digits and dashes", name)
	}
	if base, ok := a.AccountOf(name); ok {
		return fmt.Errorf("%s is already an account of %s", name, base)
	}
	if _, ok := a.FindPlugin(name); ok || a.KnownServer(name) {
		return fmt.Errorf("%q is already a plugin; choose another name", name)
	}
	if base, ok := a.AccountOf(of); ok {
		return fmt.Errorf("%s is itself an account of %s: add an account of %s", of, base, base)
	}
	if !a.SignsIn(of) {
		if !a.KnownServer(of) {
			return fmt.Errorf("no plugin %q", of)
		}
		return fmt.Errorf("%s has no credentials of its own: an account of it would be the same server again", of)
	}
	return config.SetAccount(a.Layout.Config(), name, of)
}

// SignsIn reports whether a plugin or server signs in to something: an
// in-process plugin's secrets, a server's OAuth sign-in or the secrets it
// references.
func (a *App) SignsIn(name string) bool {
	if p, ok := a.InProcess(name); ok {
		return len(p.InProcess.Secrets) > 0
	}
	srv, ok := a.Config.MCPServers[name]
	return ok && config.SignsIn(srv)
}

// RemoveAccount removes an account from config, then forgets every
// credential it has (its OAuth tokens and secrets, its app's client
// credentials too): only its own, never its plugin's.
func (a *App) RemoveAccount(name string) error {
	if _, ok := a.AccountOf(name); !ok {
		return fmt.Errorf("%s isn't an account", name)
	}
	if _, err := config.RemoveAccount(a.Layout.Config(), name); err != nil {
		return err
	}
	s, ok := a.FindSignIn(name)
	if !ok {
		return nil
	}
	if err := a.forget(s, true); err != nil {
		return errors.Join(fmt.Errorf("removed %s, but couldn't forget its credentials", name), err)
	}
	return nil
}
