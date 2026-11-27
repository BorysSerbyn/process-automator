//go:build unix

package engine

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// fakeApps is which apps' processes run, for a test: opening one starts it
// unless it's broken.
type fakeApps struct {
	mu      sync.Mutex
	running map[string]bool
	opened  []string
	broken  bool
}

func useFakeApps(t *testing.T) *fakeApps {
	f := &fakeApps{running: map[string]bool{}}
	undo := SetAppsForTests(func(p string) bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.running[p]
	}, func(path string) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.opened = append(f.opened, path)
		if !f.broken {
			f.running[strings.TrimSuffix(filepath.Base(path), ".app")] = true
		}
		return nil
	})
	t.Cleanup(undo)
	return f
}

func (f *fakeApps) openedApps() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.opened)
}

// bridgeApp is a plugin bridging to Bridge.app (installed in a temp folder),
// and calendar, an in-process plugin that uses it.
func bridgeApp(t *testing.T) (*App, pluginapi.App) {
	t.Helper()
	app := pluginapi.App{Name: "Bridge", Path: filepath.Join(t.TempDir(), "Bridge.app"), Process: "Bridge"}
	if err := os.MkdirAll(app.Path, 0o700); err != nil {
		t.Fatal(err)
	}
	bridge := pluginapi.Plugin{Name: "bridge", App: &app, InProcess: &pluginapi.InProcess{
		Serve: func(pluginapi.Host) (*sdk.Server, error) {
			return sdk.NewServer(&sdk.Implementation{Name: "bridge"}, nil), nil
		},
	}}
	calendar := pluginapi.Plugin{Name: "calendar", InProcess: &pluginapi.InProcess{
		Serve: func(pluginapi.Host) (*sdk.Server, error) {
			return sdk.NewServer(&sdk.Implementation{Name: "calendar"}, nil), nil
		},
		Uses: func(pluginapi.Host) []string { return []string{"bridge"} },
	}}
	return permApp(t, "", bridge, calendar), app
}

func appStep(t *testing.T, a *App) *SetupStep {
	t.Helper()
	out, err := a.Setup()
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range out.Steps {
		if st.Step == StepApp {
			return &st
		}
	}
	return nil
}

// An app a plugin needs is a setup step while routines use the plugin
// (through another plugin too): done while it runs.
func TestAppsPluginsNeedAreASetupStep(t *testing.T) {
	apps := useFakeApps(t)
	a, _ := bridgeApp(t)
	if st := appStep(t, a); st != nil {
		t.Fatalf("a step with nothing using it: %+v", st)
	}
	writePrompt(t, a, "standup", "automator.servers: [calendar]\n")
	st := appStep(t, a)
	if st == nil || st.Done || st.Title != "Keep Bridge running" || st.Subject != "bridge" ||
		st.Status != "Not running; used by standup" || !strings.Contains(st.Commands[0], "plugins open-app bridge") {
		t.Fatalf("%+v", st)
	}
	apps.running["Bridge"] = true
	if st := appStep(t, a); st == nil || !st.Done || st.Status != "Running; used by standup" {
		t.Fatalf("running: %+v", st)
	}
	if len(apps.openedApps()) != 0 {
		t.Errorf("setup opened %v", apps.openedApps())
	}
}

// Connecting to a plugin that needs an app opens it first; one that isn't
// installed, or doesn't start, fails the connect at once, saying so.
func TestConnectingOpensTheAppFirst(t *testing.T) {
	apps := useFakeApps(t)
	a, app := bridgeApp(t)
	s, err := a.Connector.Connect(context.Background(), "bridge")
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if !slices.Equal(apps.openedApps(), []string{app.Path}) {
		t.Fatalf("opened %v", apps.openedApps())
	}
	if s, err := a.Connector.Connect(context.Background(), "bridge"); err != nil || len(apps.openedApps()) != 1 {
		t.Fatalf("running already: opened %v, %v", apps.openedApps(), err)
	} else {
		_ = s.Close()
	}

	apps.running["Bridge"], apps.broken = false, true
	orig := appStartWait
	appStartWait = 300 * time.Millisecond
	t.Cleanup(func() { appStartWait = orig })
	if _, err := a.Connector.Connect(context.Background(), "bridge"); err == nil ||
		!strings.Contains(err.Error(), "connect bridge: Bridge isn't running: it didn't start within 300ms") {
		t.Fatalf("didn't start: %v", err)
	}
	if err := os.RemoveAll(app.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Connector.Connect(context.Background(), "bridge"); err == nil || !strings.Contains(err.Error(), "isn't installed") {
		t.Fatalf("not installed: %v", err)
	}
}

// The scheduler keeps the app open while a routine that's on uses it, not
// for one that's off, tries again only after a while, and not at all with
// plugins.open-apps off.
func TestTheSchedulerKeepsAppsOpen(t *testing.T) {
	apps := useFakeApps(t)
	a, app := bridgeApp(t)
	x := writePrompt(t, a, "standup", "automator.servers: [calendar]\n")
	set := func(key, value string) {
		t.Helper()
		if _, err := (settings.Store{Path: a.Layout.Config(), Extra: x.Settings()}).Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	set("routines.standup.enabled", "false")
	k := &appKeeper{}
	now := time.Now()
	k.keep(context.Background(), a.reloaded(t), now)
	if len(apps.openedApps()) != 0 {
		t.Fatalf("opened for a routine that's off: %v", apps.openedApps())
	}
	set("routines.standup.enabled", "true")
	a = a.reloaded(t)
	x, _ = a.Find("standup")
	if err := a.Trust(x); err != nil {
		t.Fatal(err)
	}
	apps.broken = true
	k.keep(context.Background(), a, now)
	if !slices.Equal(apps.openedApps(), []string{app.Path}) {
		t.Fatalf("opened %v", apps.openedApps())
	}
	k.keep(context.Background(), a, now.Add(time.Minute))
	if len(apps.openedApps()) != 1 {
		t.Fatalf("tried again at once: %v", apps.openedApps())
	}
	k.keep(context.Background(), a, now.Add(appRetry))
	if len(apps.openedApps()) != 2 {
		t.Fatalf("didn't try again: %v", apps.openedApps())
	}
	set("plugins.open-apps", "false")
	k.keep(context.Background(), a.reloaded(t), now.Add(3*appRetry))
	if len(apps.openedApps()) != 2 {
		t.Fatalf("opened with plugins.open-apps off: %v", apps.openedApps())
	}
}
