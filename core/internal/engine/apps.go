package engine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// Apps plugins need running (pluginapi.Plugin.App): setup's step while one
// isn't, opening one before connecting to its plugin, and the scheduler
// keeping them open (plugins.open-apps).

// appStartWait is how long opening an app waits for its process to show.
var appStartWait = 20 * time.Second

// appRetry is how soon the scheduler tries again to open an app that it
// opened and that still isn't running (one that quits at once, say).
const appRetry = 5 * time.Minute

// NeededApp is an app plugins need running, and who uses it.
type NeededApp struct {
	pluginapi.App
	// Plugins are the plugins that need it, sorted.
	Plugins []string
	// Users are the routines and hooks that use those plugins, sorted.
	Users []string
	// on are the users that are on (enabled and trusted): the scheduler
	// keeps the app open for them.
	on bool
}

// AppOf is the app the plugin (or account of one) named server needs, if any.
func (a *App) AppOf(server string) (pluginapi.App, bool) {
	for _, p := range a.Plugins {
		if p.Name == server && p.App != nil {
			return *p.App, true
		}
	}
	return pluginapi.App{}, false
}

// NeededApps are the apps the routines and hooks use (through their plugins,
// and those plugins' plugins), by name.
func (a *App) NeededApps() ([]NeededApp, error) {
	all, _, err := a.All()
	if err != nil {
		return nil, err
	}
	hooks, _, err := a.Hooks()
	if err != nil {
		return nil, err
	}
	byName := map[string]*NeededApp{}
	for _, x := range append(all, hooks...) {
		on := a.isOn(x)
		for _, s := range x.reaches(a) {
			app, ok := a.AppOf(s)
			if !ok {
				continue
			}
			n := byName[app.Name]
			if n == nil {
				n = &NeededApp{App: app}
				byName[app.Name] = n
			}
			if !slices.Contains(n.Plugins, s) {
				n.Plugins = append(n.Plugins, s)
			}
			if !slices.Contains(n.Users, x.Name()) {
				n.Users = append(n.Users, x.Name())
			}
			n.on = n.on || on
		}
	}
	out := make([]NeededApp, 0, len(byName))
	for _, n := range byName {
		sort.Strings(n.Plugins)
		sort.Strings(n.Users)
		out = append(out, *n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// isOn reports whether x runs by itself: enabled and trusted.
func (a *App) isOn(x Runnable) bool {
	cfg, err := x.runConfig(a)
	if err != nil || !cfg.Enabled {
		return false
	}
	why, _, err := a.TrustProblem(x)
	return err == nil && why == ""
}

// AppRunning reports whether app is running.
func AppRunning(app pluginapi.App) bool { return appRunning(app.Process) }

// OpenApp opens app unless it's running, and waits for its process; the
// error says why it isn't running (not installed, didn't start).
func OpenApp(ctx context.Context, app pluginapi.App) error {
	if appRunning(app.Process) {
		return nil
	}
	if _, err := os.Stat(app.Path); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s isn't running, and isn't installed (no %s)", app.Name, app.Path)
	}
	if err := openApp(app.Path); err != nil {
		return fmt.Errorf("%s isn't running, and opening it failed: %w", app.Name, err)
	}
	deadline := time.Now().Add(appStartWait)
	for !appRunning(app.Process) {
		if time.Now().After(deadline) {
			return fmt.Errorf("%s isn't running: it didn't start within %s of opening it", app.Name, appStartWait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return nil
}

// beforeConnect opens the app the server's plugin needs before connecting
// to it: a server bridging to an app that's quit waits for it, never
// answering.
func (a *App) beforeConnect(ctx context.Context, server string) error {
	if app, ok := a.AppOf(server); ok {
		return OpenApp(ctx, app)
	}
	return nil
}

// appKeeper is the scheduler's: it opens the apps that routines and hooks
// that are on need, trying an app it opened that still isn't running again
// only after appRetry.
type appKeeper struct {
	mu    sync.Mutex
	tried map[string]time.Time
}

func (k *appKeeper) keep(ctx context.Context, a *App, now time.Time) {
	vals, err := a.Settings()
	if err != nil || !vals.Bool("plugins.open-apps") {
		return
	}
	apps, err := a.NeededApps()
	if err != nil {
		logf("apps: %v", err)
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.tried == nil {
		k.tried = map[string]time.Time{}
	}
	for _, n := range apps {
		if !n.on || appRunning(n.Process) || now.Sub(k.tried[n.Name]) < appRetry {
			continue
		}
		k.tried[n.Name] = now
		if err := OpenApp(ctx, n.App); err != nil {
			logf("apps: %v", err)
			continue
		}
		logf("apps: opened %s (%s use it)", n.Name, strings.Join(n.Users, ", "))
	}
}

// appSteps is a step per app the routines and hooks need: done while it's
// running.
func (a *App) appSteps() ([]SetupStep, error) {
	apps, err := a.NeededApps()
	if err != nil {
		return nil, err
	}
	steps := make([]SetupStep, 0, len(apps))
	for _, n := range apps {
		running := AppRunning(n.App)
		who := strings.Join(n.Users, ", ")
		st := SetupStep{Step: StepApp, Subject: n.Plugins[0], Title: "Keep " + n.Name + " running", Done: running,
			Status:   "Running; used by " + who,
			Ask:      "Open " + n.Name + "? " + who + " need it running.",
			Commands: []string{product.Command + " plugins open-app " + n.Plugins[0]}}
		if !running {
			st.Status = "Not running; used by " + who
		}
		steps = append(steps, st)
	}
	return steps, nil
}

// SetAppsForTests replaces looking for an app's process and opening one,
// for other packages' tests (none ever opens a real app); undo puts them
// back.
func SetAppsForTests(running func(process string) bool, open func(path string) error) (undo func()) {
	r, o := appRunning, openApp
	appRunning, openApp = running, open
	return func() { appRunning, openApp = r, o }
}
