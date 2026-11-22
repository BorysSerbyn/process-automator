package engine

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/mcp"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/programs"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/trust"
	"github.com/BorysSerbyn/process-automator/core/internal/watchers"
)

// Watchers are programs the daemon runs to hear about events, for hooks.
// Like a program, a watcher is trusted by checksum, and what runs is the
// app's copy; it runs while it's trusted, not paused, and a hook that's on
// listens to it.

// Watchers are every watcher, and those that don't load. A name a routine or
// a hook has too is broken for the watcher.
func (a *App) Watchers() ([]watchers.Watcher, []Broken, error) {
	set, err := watchers.Load(a.Layout.Watchers())
	if err != nil {
		return nil, nil, err
	}
	var out []watchers.Watcher
	var broken []Broken
	for _, e := range set.Errors {
		broken = append(broken, Broken{Kind: KindProgram, Name: e.Name, Path: e.Path, Err: e.Err})
	}
	for _, w := range set.Watchers {
		if why := a.takenElsewhere(w.Name, "watcher"); why != "" {
			broken = append(broken, Broken{Kind: KindProgram, Name: w.Name, Path: w.Path, Err: fmt.Errorf("%s", why)})
			continue
		}
		out = append(out, w)
	}
	return out, broken, nil
}

// FindWatcher is the watcher named name.
func (a *App) FindWatcher(name string) (watchers.Watcher, error) {
	all, broken, err := a.Watchers()
	if err != nil {
		return watchers.Watcher{}, err
	}
	for _, w := range all {
		if w.Name == name {
			return w, nil
		}
	}
	for _, b := range broken {
		if b.Name == name {
			return watchers.Watcher{}, b
		}
	}
	return watchers.Watcher{}, fmt.Errorf("no watcher %q", name)
}

// WatcherSecretKey is the secret-store key of a watcher's secret.
func WatcherSecretKey(watcher, name string) string { return "watcher/" + watcher + "/" + name }

// watcherSecret is one of a watcher's secrets as a sign-in asks for it.
func watcherSecret(w watchers.Watcher, s programs.Secret) Secret {
	label := s.Prompt
	if label == "" {
		label = s.Name
	}
	return Secret{Key: WatcherSecretKey(w.Name, s.Name), Label: label}
}

// WatcherApproval is what trusting a watcher approves: its executable and
// description file by checksum, and its secrets.
func (a *App) WatcherApproval(w watchers.Watcher) (trust.Approval, error) {
	sum, err := programs.Checksum(w.Program.Executable)
	if err != nil {
		return trust.Approval{}, fmt.Errorf("its executable can't be read: %w", err)
	}
	tp := &trust.Program{Executable: sum, Description: programs.ChecksumText(w.Source)}
	for _, s := range w.Program.Secrets {
		tp.Secrets = append(tp.Secrets, s.Name)
	}
	return trust.Approval{Watcher: tp, Permissions: w.Program.Permissions}, nil
}

// TrustWatcher records a watcher as trusted with what it is now.
func (a *App) TrustWatcher(w watchers.Watcher) error {
	ap, err := a.WatcherApproval(w)
	if err != nil {
		return err
	}
	return a.TrustStore().Trust(map[string]trust.Approval{w.Name: ap}, time.Now())
}

// WatcherUntrusted says why a watcher may not run ("" when it may): it was
// never trusted, or isn't what was.
func (a *App) WatcherUntrusted(w watchers.Watcher) (string, error) {
	f, _, err := a.TrustStore().Load()
	if err != nil {
		return "", err
	}
	rec, ok := f.Routines[w.Name]
	if !ok {
		return "not trusted yet", nil
	}
	cur, err := a.WatcherApproval(w)
	if err != nil {
		return "", err
	}
	if rec.Approves(cur) {
		return "", nil
	}
	return "changed since you trusted it: " + strings.Join(rec.ChangesTo(cur), "; "), nil
}

// WatcherEnabled is whether a watcher isn't paused (watchers.<name>.enabled).
func (a *App) WatcherEnabled(w watchers.Watcher) (bool, error) {
	vals, err := (settings.Store{Path: a.Layout.Config(), Extra: settings.WatcherSettings(w.Name)}).Values()
	if err != nil {
		return false, err
	}
	return vals.Bool("watchers." + w.Name + ".enabled"), nil
}

// watcherInvocation is how a watcher runs: the app's copy, checked against
// what was trusted right before it starts, with a program's environment (a
// fixed PATH, HOME, its secrets), in its own folder.
func (a *App) watcherInvocation(w watchers.Watcher) (programs.Invocation, error) {
	trusted, _, err := a.TrustStore().Load()
	if err != nil {
		return programs.Invocation{}, err
	}
	sum, err := programs.Checksum(w.Program.Executable)
	if err != nil {
		return programs.Invocation{}, fmt.Errorf("its executable can't be read: %w", err)
	}
	if rec, ok := trusted.Routines[w.Name]; !ok || rec.Watcher == nil || rec.Watcher.Executable != sum {
		return programs.Invocation{}, fmt.Errorf("its executable isn't the one you trusted: review it with `%s watchers trust %s`", product.Command, w.Name)
	}
	vars := map[string]string{}
	for _, s := range w.Program.Secrets {
		v, stored, err := a.Stored(watcherSecret(w, s))
		switch {
		case err != nil:
			return programs.Invocation{}, fmt.Errorf("its secret %s can't be read: %w", s.Name, err)
		case !stored:
			return programs.Invocation{}, &mcp.NeedsReauthError{Server: w.Name, Cause: fmt.Errorf("its secret %s isn't stored", s.Name)}
		}
		vars[s.Env] = v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return programs.Invocation{}, err
	}
	dir := a.Layout.WatcherData(w.Name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return programs.Invocation{}, err
	}
	return programs.Invocation{Executable: w.Program.Executable, Args: w.Program.Args, Dir: dir, Env: programs.Environment(home, vars)}, nil
}
