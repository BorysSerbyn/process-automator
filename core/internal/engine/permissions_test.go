//go:build unix

package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BorysSerbyn/process-automator/core/internal/history"
	"github.com/BorysSerbyn/process-automator/core/internal/hooks"
	"github.com/BorysSerbyn/process-automator/core/internal/permissions"
	"github.com/BorysSerbyn/process-automator/core/internal/programs"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
	"github.com/BorysSerbyn/process-automator/core/internal/secrets"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/trigger"
	"github.com/BorysSerbyn/process-automator/core/internal/watchers"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// fakeOS is what the OS says of each permission, for a test; an
// unlisted one isn't checked on this OS. Asking for one grants it.
type fakeOS struct {
	mu     sync.Mutex
	states map[string]permissions.Status
	asked  []string
}

func fakePermissions(t *testing.T, states map[string]permissions.Status) *fakeOS {
	f := &fakeOS{states: states}
	check, canRequest, request, pane := checkPermission, canRequestPermission, requestPermission, permissionPane
	checkPermission = func(name string) permissions.Status {
		f.mu.Lock()
		defer f.mu.Unlock()
		if s, ok := f.states[name]; ok {
			return s
		}
		return permissions.Unsupported
	}
	canRequestPermission = func(name string) (bool, string) {
		if name == "full-disk-access" {
			return false, "no way to ask"
		}
		return true, ""
	}
	requestPermission = func(name string) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.asked = append(f.asked, name)
		f.states[name] = permissions.Granted
	}
	permissionPane = func(name string) (string, string) { return "in Settings > " + name, "x-test:" + name }
	t.Cleanup(func() {
		checkPermission, canRequestPermission, requestPermission, permissionPane = check, canRequest, request, pane
	})
	return f
}

func (f *fakeOS) set(name string, s permissions.Status) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[name] = s
}

// stubPlugin is an in-process plugin declaring perms.
func stubPlugin(name string, perms ...string) pluginapi.Plugin {
	return pluginapi.Plugin{Name: name, Permissions: perms, InProcess: &pluginapi.InProcess{
		Serve: func(pluginapi.Host) (*sdk.Server, error) {
			return sdk.NewServer(&sdk.Implementation{Name: name, Version: "1"}, nil), nil
		},
	}}
}

// permApp is an app whose config is config, with plugins.
func permApp(t *testing.T, config string, plugins ...pluginapi.Plugin) *App {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := Load(path, plugins, secrets.NewMemory())
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func writePrompt(t *testing.T, a *App, name, front string) Runnable {
	t.Helper()
	if err := os.MkdirAll(a.Layout.Routines(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.Layout.Routines(), name+".prompt"), []byte("---\n"+front+"---\nGo."), 0o600); err != nil {
		t.Fatal(err)
	}
	x, err := a.Find(name)
	if err != nil {
		t.Fatal(err)
	}
	return x
}

func (a *App) reloaded(t *testing.T) *App {
	t.Helper()
	b, err := Load(a.Layout.Config(), a.Plugins, a.Connector.Secrets)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func needNames(ns []PermissionNeed) string {
	var out []string
	for _, n := range ns {
		var by []string
		for _, s := range n.By {
			by = append(by, s.Kind+":"+s.Name)
		}
		out = append(out, n.Permission+"="+string(n.State)+"("+strings.Join(by, ",")+")")
	}
	return strings.Join(out, " ")
}

func problemsAbout(rd Readiness) map[string]Problem {
	out := map[string]Problem{}
	for _, p := range rd.Problems {
		if p.Permission != "" {
			out[p.Permission] = p
		}
	}
	return out
}

// A prompt declares no permissions: it needs what the plugins it uses
// declare (a compiled-in plugin's, a config server's), shown with each
// plugin. Until the scheduler has checked them they're "not checked",
// never granted, and not a problem either (a CLI can't check them, and the
// scheduler checks before it decides anything); once checked, one that
// isn't allowed is a problem, naming the permission and where it's turned on.
func TestPromptNeedsItsPluginsPermissions(t *testing.T) {
	fake := fakePermissions(t, map[string]permissions.Status{"input-monitoring": permissions.Missing, "calendars": permissions.Granted})
	a := permApp(t, "mcpServers:\n  cal:\n    command: /bin/true\n    permissions: [calendars]\n", stubPlugin("keys", "input-monitoring"), stubPlugin("plain"))
	x := writePrompt(t, a, "digest", "automator.servers: [keys, cal, plain]\n")
	if got := needNames(a.Permissions(x)); got != "input-monitoring=not-checked(plugin:keys) calendars=not-checked(plugin:cal)" {
		t.Fatalf("before any check: %s", got)
	}
	if probs := problemsAbout(a.Assess(x)); len(probs) != 0 {
		t.Fatalf("not checked yet isn't a problem: %+v", probs)
	}

	if _, err := a.RecordPermissions(time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := needNames(a.Permissions(x)); got != "input-monitoring=missing(plugin:keys) calendars=granted(plugin:cal)" {
		t.Fatalf("once checked: %s", got)
	}
	probs := problemsAbout(a.Assess(x))
	p, ok := probs["input-monitoring"]
	if len(probs) != 1 || !ok || p.Open != "x-test:input-monitoring" ||
		!strings.Contains(p.Text, "its plugin keys needs the Input Monitoring permission") ||
		!strings.Contains(p.Fix, "permissions request input-monitoring") || !strings.Contains(p.Fix, "in Settings > input-monitoring") {
		t.Fatalf("a missing permission: %+v", probs)
	}
	// Setup's step for it asks the OS.
	_, steps, err := a.loadRoutines()
	if err != nil || len(steps) != 1 || steps[0].Commands[0] != "procauto permissions request input-monitoring" {
		t.Fatalf("setup: %+v %v", steps, err)
	}
	// One that can't be checked is never taken for missing.
	fake.set("input-monitoring", permissions.Unknown)
	_, _ = a.RecordPermissions(time.Now())
	if probs := problemsAbout(a.Assess(x)); len(probs) != 0 {
		t.Fatalf("an unknown permission is a problem: %+v", probs)
	}
	ap, err := a.Approval(x)
	if err != nil || !slices.Equal(ap.Permissions, []string{"calendars", "input-monitoring"}) {
		t.Fatalf("trusting it covers its plugins' permissions: %v %v", ap.Permissions, err)
	}
}

// A missing permission keeps a scheduled routine from being scheduled:
// health isn't green before its first run.
func TestMissingPermissionIsAScheduleProblem(t *testing.T) {
	fakePermissions(t, map[string]permissions.Status{"notifications": permissions.Missing})
	a := permApp(t, "", stubPlugin("notify", "notifications"))
	x := writePrompt(t, a, "digest", "automator.servers: [notify]\nautomator.schedule: \"0 9 * * *\"\n")
	if err := a.InitTrust(); err != nil {
		t.Fatal(err)
	}
	_, _ = a.RecordPermissions(time.Now())
	if _, err := (settings.Store{Path: a.Layout.Config(), Extra: x.Settings()}).Set("routines.digest.enabled", "true"); err != nil {
		t.Fatal(err)
	}
	_, problems, err := scheduledJobs(a)
	if err != nil || !strings.Contains(problems["digest"], "Notifications permission") {
		t.Fatalf("%v %v", problems, err)
	}
}

// A hook needs its action's permissions (a program's own, or a prompt's
// plugins') and its watcher's; trusting it covers its action's, and its
// watcher's trust covers the watcher's.
func TestHookNeedsItsActionsAndWatchersPermissions(t *testing.T) {
	fakePermissions(t, map[string]permissions.Status{"input-monitoring": permissions.Missing, "accessibility": permissions.Missing})
	a := permApp(t, "", stubPlugin("notify", "notifications"))
	w := addWatcher(t, a, "keys", "executable: ./watch\nmode: stream\npermissions: [input-monitoring]\n", "sleep 60\n")
	addHook(t, a, "click", hooks.DescriptionFile,
		"executable: ./act\nwrites: false\non: {watcher: keys}\npermissions: [accessibility]\n", "true\n")
	addHook(t, a, "tell", "tell.prompt",
		"---\nautomator.on: {watcher: keys}\nautomator.servers: [notify]\ntools: [notify_me]\n---\nTell me.", "")
	click, _ := a.FindHook("click")
	tell, _ := a.FindHook("tell")
	if got := needNames(a.Permissions(click)); got != "input-monitoring=not-checked(watcher:keys) accessibility=not-checked(hook:click)" {
		t.Fatalf("a program hook: %s", got)
	}
	if got := needNames(a.Permissions(tell)); got != "input-monitoring=not-checked(watcher:keys) notifications=not-checked(plugin:notify)" {
		t.Fatalf("an LLM hook: %s", got)
	}
	if probs := problemsAbout(a.Assess(click)); len(probs) != 0 {
		t.Fatalf("not checked yet isn't a problem: %+v", probs)
	}
	if _, err := a.RecordPermissions(time.Now()); err != nil {
		t.Fatal(err)
	}
	probs := problemsAbout(a.Assess(click))
	if !strings.HasPrefix(probs["input-monitoring"].Text, "its watcher keys needs") || !strings.HasPrefix(probs["accessibility"].Text, "it needs") {
		t.Fatalf("%+v", probs)
	}
	if ap, _ := a.Approval(click); !slices.Equal(ap.Permissions, []string{"accessibility"}) {
		t.Fatalf("the hook's approval: %v", ap.Permissions)
	}
	if ap, _ := a.WatcherApproval(w); !slices.Equal(ap.Permissions, []string{"input-monitoring"}) {
		t.Fatalf("the watcher's approval: %v", ap.Permissions)
	}
}

// Trust covers permissions: one added to a program, or to a plugin a
// routine uses, takes its trust away until it's reviewed again.
func TestTrustCoversPermissions(t *testing.T) {
	fakePermissions(t, map[string]permissions.Status{})
	a := permApp(t, "mcpServers:\n  cal:\n    command: /bin/true\n")
	x := writePrompt(t, a, "agenda", "automator.servers: [cal]\n")
	if err := a.InitTrust(); err != nil {
		t.Fatal(err)
	}
	if why, _ := a.Untrusted(x); why != "" {
		t.Fatalf("trusted at first use: %s", why)
	}
	if err := os.WriteFile(a.Layout.Config(), []byte("mcpServers:\n  cal:\n    command: /bin/true\n    permissions: [calendars]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := a.reloaded(t)
	x, _ = b.Find("agenda")
	if why, _ := b.Untrusted(x); !strings.Contains(why, "needs the calendars permission now") {
		t.Fatalf("a plugin's permission added: %q", why)
	}
	if err := b.Trust(x); err != nil {
		t.Fatal(err)
	}
	if why, _ := b.Untrusted(x); why != "" {
		t.Fatalf("reviewed again: %s", why)
	}

	// A program: its file's permissions.
	src := t.TempDir()
	writeExe(t, src, "prog", "true\n")
	desc := filepath.Join(src, programs.DescriptionFile)
	_ = os.WriteFile(desc, []byte("executable: ./prog\nwrites: false\n"), 0o600)
	if _, err := programs.Add(b.Layout.Programs(), "snap", desc, false); err != nil {
		t.Fatal(err)
	}
	p, _ := b.Find("snap")
	_ = b.Trust(p)
	_ = os.WriteFile(desc, []byte("executable: ./prog\nwrites: false\npermissions: [screen-recording]\n"), 0o600)
	if _, err := programs.Add(b.Layout.Programs(), "snap", desc, true); err != nil {
		t.Fatal(err)
	}
	p, _ = b.Find("snap")
	why, _ := b.Untrusted(p)
	if !strings.Contains(why, "needs the screen-recording permission now") || strings.Contains(why, "description file") {
		t.Fatalf("a program's permission added: %q", why)
	}
}

// What was trusted before permissions were declared covers the ones it
// needs then: they're recorded once (by InitTrust, which edits, runs and
// the scheduler call), and one added after takes the trust away.
func TestTrustFromBeforePermissions(t *testing.T) {
	fakePermissions(t, map[string]permissions.Status{})
	a := permApp(t, "mcpServers:\n  cal:\n    command: /bin/true\n    permissions: [calendars]\n")
	x := writePrompt(t, a, "agenda", "automator.servers: [cal]\n")
	old := `{"schemaVersion":1,"routines":{"agenda":{"surface":{"servers":["cal"],"tools":[]},"trustedAt":"2026-11-01T09:00:00Z"}}}`
	if err := os.WriteFile(a.Layout.Trust(), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if why, _ := a.Untrusted(x); why != "" {
		t.Fatalf("an old record: %s", why)
	}
	if err := a.InitTrust(); err != nil {
		t.Fatal(err)
	}
	f, _, _ := a.TrustStore().Load()
	if rec := f.Routines["agenda"]; rec.Unrecorded() || !slices.Equal(rec.Permissions, []string{"calendars"}) {
		t.Fatalf("recorded: %+v", rec)
	}
	_ = os.WriteFile(a.Layout.Config(), []byte("mcpServers:\n  cal:\n    command: /bin/true\n    permissions: [calendars, contacts]\n"), 0o600)
	b := a.reloaded(t)
	x, _ = b.Find("agenda")
	if why, _ := b.Untrusted(x); !strings.Contains(why, "needs the contacts permission now") {
		t.Fatalf("one added after: %q", why)
	}
}

// A watcher isn't started while a permission it declares isn't granted
// (its hooks say why); once the scheduler finds it granted, it starts.
func TestWatcherWaitsForItsPermissions(t *testing.T) {
	fake := fakePermissions(t, map[string]permissions.Status{"input-monitoring": permissions.Missing})
	a := testApp(t)
	addWatcher(t, a, "keys", "executable: ./watch\nmode: poll\ninterval: 5s\npermissions: [input-monitoring]\n",
		`echo '{"id":"k1","type":"key.pressed"}'`+"\n")
	addHook(t, a, "on-key", hooks.DescriptionFile, "executable: ./act\nwrites: false\non: {watcher: keys}\n", "cat >> got.jsonl\n")
	_, _ = a.RecordPermissions(time.Now())
	e, stop := startEvents(t, a)
	defer stop()
	running := func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		_, ok := e.running["keys"]
		return ok
	}
	if running() {
		t.Fatal("started without its permission")
	}
	x, _ := a.FindHook("on-key")
	if probs := problemsAbout(a.Assess(x)); !strings.Contains(probs["input-monitoring"].Text, "its watcher keys needs the Input Monitoring permission") {
		t.Fatalf("the hook's problem: %+v", probs)
	}
	setups := hookSetups(a)
	if setups["on-key"].Watcher != "keys" || !strings.HasPrefix(setups["on-key"].WatcherProblem, "keys needs the Input Monitoring permission") ||
		setups["on-key"].Problem != "" {
		t.Fatalf("run status: %+v", setups["on-key"])
	}

	fake.set("input-monitoring", permissions.Granted)
	_, _ = a.RecordPermissions(time.Now())
	e.sync(context.Background(), a)
	if !running() {
		t.Fatal("not started once granted")
	}
	waitUntil(t, "its event", func() bool { return slices.Equal(gotEvents(a, "on-key"), []string{"k1"}) })
}

// The report lists what's needed, by what, with what the scheduler
// recorded: nothing recorded is "not checked", never granted.
func TestPermissionReport(t *testing.T) {
	fakePermissions(t, map[string]permissions.Status{"notifications": permissions.Granted})
	a := permApp(t, "", stubPlugin("notify", "notifications"))
	writePrompt(t, a, "digest", "automator.servers: [notify]\n")
	addWatcher(t, a, "keys", "executable: ./watch\nmode: stream\npermissions: [input-monitoring]\n", "sleep 60\n")
	r, err := a.PermissionReport(false)
	if err != nil {
		t.Fatal(err)
	}
	if r.CheckedAt != nil || len(r.Permissions) != 2 {
		t.Fatalf("%+v", r)
	}
	for _, p := range r.Permissions {
		if p.State != permissions.NotChecked {
			t.Fatalf("nothing recorded reads %s", p.State)
		}
	}
	n := r.Permissions[1]
	if n.Permission != "notifications" || len(n.NeededBy) != 1 || n.NeededBy[0].Kind != UseRoutine || n.NeededBy[0].Name != "digest" ||
		n.NeededBy[0].By[0] != (PermissionSource{SourcePlugin, "notify"}) {
		t.Fatalf("%+v", n)
	}
	_, _ = a.RecordPermissions(time.Now())
	if r, _ = a.PermissionReport(true); r.CheckedAt == nil || len(r.Permissions) != len(permissions.All) {
		t.Fatalf("all: %+v", r)
	}
	for _, p := range r.Permissions {
		want := permissions.Unsupported
		if p.Permission == "notifications" {
			want = permissions.Granted
		}
		if p.State != want {
			t.Errorf("%s: %s", p.Permission, p.State)
		}
	}
}

// A request goes through the scheduler, which asks the OS (macOS
// attributes it to the app) and records the answer; without a running
// scheduler nothing is asked. An unknown permission is refused.
func TestRequestPermission(t *testing.T) {
	fake := fakePermissions(t, map[string]permissions.Status{"input-monitoring": permissions.Missing, "full-disk-access": permissions.Unknown})
	a := testApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := a.RequestPermission(ctx, "keyboard", time.Now()); err == nil {
		t.Fatal("an unknown permission")
	}
	out, err := a.RequestPermission(ctx, "input-monitoring", time.Now())
	if err != nil || out.Asked || !strings.Contains(out.Why, "scheduler isn't running") || out.SettingsURL != "x-test:input-monitoring" {
		t.Fatalf("no scheduler: %+v %v", out, err)
	}
	if err := runstatus.Heartbeat(a.Layout.RunStatus(), time.Now(), time.Minute); err != nil {
		t.Fatal(err)
	}
	go servePermissionRequests(ctx, a, newEvents(a, nil), func() (*App, error) { return a, nil }, newFollower(newWaker()))
	out, err = a.RequestPermission(ctx, "input-monitoring", time.Now())
	if err != nil || !out.Asked || out.State != permissions.Granted {
		t.Fatalf("asked: %+v %v", out, err)
	}
	if !slices.Equal(fake.asked, []string{"input-monitoring"}) {
		t.Fatalf("the OS was asked for %v", fake.asked)
	}
	out, err = a.RequestPermission(ctx, "full-disk-access", time.Now())
	if err != nil || out.Asked || out.Why != "no way to ask" {
		t.Fatalf("one the OS can't be asked for: %+v %v", out, err)
	}
}

// reasonTexts are run status's reasons, after bringing it up to date.
func reasonTexts(t *testing.T, a *App) (*runstatus.Status, []string) {
	t.Helper()
	if err := a.SyncStatus(time.Now()); err != nil {
		t.Fatal(err)
	}
	s, err := runstatus.Load(a.Layout.RunStatus())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range s.Reasons {
		out = append(out, r.Text)
	}
	return s, out
}

// Input Monitoring taken away, then given back: each change resets the
// health of the watcher that needs it and the hooks listening (its error
// cleared, their last runs no longer counted), so once it's granted again
// they're neutral, not red from before; while it's missing, that's the one
// problem. Their runs stay in history. A failure after turns them red again.
func TestAPermissionChangeResetsHealth(t *testing.T) {
	fake := fakePermissions(t, map[string]permissions.Status{"input-monitoring": permissions.Granted})
	a := testApp(t)
	addWatcher(t, a, "home-key", "executable: ./watch\nmode: stream\npermissions: [input-monitoring]\n", "sleep 60\n")
	addHook(t, a, "home-joke", hooks.DescriptionFile, "executable: ./act\nwrites: false\non: {watcher: home-key}\n", "true\n")
	addHook(t, a, "say-hi", hooks.DescriptionFile, "executable: ./act\nwrites: false\non: {watcher: home-key}\n", "true\n")
	e := newEvents(a, func() (*App, error) { return a, nil }) // its watchers aren't started: failures are given
	ctx := context.Background()
	recordPermissions(a, e, time.Now()) // the first check: nothing to compare with
	if _, reasons := reasonTexts(t, a); len(reasons) != 0 {
		t.Fatalf("at first: %v", reasons)
	}
	before := time.Now().Add(-time.Second)
	recordRun(a, "home-joke", trigger.Occurrence{Trigger: trigger.Event, At: before}, runstatus.Run{
		StartedAt: before, FinishedAt: &before, Outcome: runstatus.OutcomeFailed, Reason: "not allowed to listen"}, nil)
	keyboard := errors.New("its stream ended: can't listen to the keyboard")
	e.handle(ctx, "home-key", nil, nil, keyboard)
	failing := "home-key is failing: " + keyboard.Error()
	s, reasons := reasonTexts(t, a)
	if !slices.Equal(reasons, []string{"home-joke failed", failing + " (home-joke and say-hi listen to it)"}) {
		t.Fatalf("failing: %q", reasons)
	}
	if r := s.Reasons[1]; r.Watcher != "home-key" || r.Routine != "" {
		t.Fatalf("the watcher's reason: %+v", r)
	}
	// Each hook still says it waits on its watcher, and why.
	for _, name := range []string{"home-joke", "say-hi"} {
		x, _ := a.FindHook(name)
		if probs := a.Assess(x).All(); len(probs) != 1 || probs[0].Text != "its watcher "+failing || probs[0].Watcher != "home-key" {
			t.Fatalf("%s's problems: %+v", name, probs)
		}
		for _, h := range s.Hooks {
			if h.Name == name && h.Problem != "its watcher "+failing {
				t.Fatalf("%s in run status: %q", name, h.Problem)
			}
		}
	}

	// Taken away: the watcher's error and the hook's failed run are from
	// before; what's missing now is the one problem.
	fake.set("input-monitoring", permissions.Missing)
	recordPermissions(a, e, time.Now())
	s, reasons = reasonTexts(t, a)
	if len(reasons) != 1 || !strings.HasPrefix(reasons[0], "home-key needs the Input Monitoring permission") ||
		!strings.HasSuffix(reasons[0], "(home-joke and say-hi listen to it)") || s.Health != runstatus.HealthError {
		t.Fatalf("taken away: %s %q", s.Health, reasons)
	}
	if st, _ := watchers.LoadState(a.Layout.WatcherState()); st.Watchers["home-key"].Error != "" {
		t.Fatalf("the watcher's error stayed: %q", st.Watchers["home-key"].Error)
	}

	// Given back: neutral, until something happens.
	fake.set("input-monitoring", permissions.Granted)
	recordPermissions(a, e, time.Now())
	s, reasons = reasonTexts(t, a)
	if len(reasons) != 0 || s.Health != runstatus.HealthOK {
		t.Fatalf("given back: %s %q", s.Health, reasons)
	}
	for _, h := range s.Hooks {
		if h.Health != runstatus.HealthNone || h.Summary != "Permissions changed; waiting for events" {
			t.Errorf("%s: %s %q, want neutral", h.Name, h.Health, h.Summary)
		}
	}
	// The run stays, in run status and history.
	if last := s.LastRun("home-joke"); last == nil || last.Outcome != runstatus.OutcomeFailed || last.Reason != "not allowed to listen" {
		t.Fatalf("its last run: %+v", last)
	}
	if runs, err := history.List(a.Layout.History(), history.Filter{Routine: "home-joke"}); err != nil || len(runs) != 1 || runs[0].Outcome != runstatus.OutcomeFailed {
		t.Fatalf("history: %+v %v", runs, err)
	}
	// Nothing changed since: the next check resets nothing.
	if changed, _ := a.RecordPermissions(time.Now()); len(changed) != 0 {
		t.Fatalf("no change, but %v", changed)
	}

	// A failure after the reset counts: the watcher's, and a run's.
	e.handle(ctx, "home-key", nil, nil, keyboard)
	time.Sleep(time.Millisecond)
	now := time.Now()
	recordRun(a, "say-hi", trigger.Occurrence{Trigger: trigger.Event, At: now}, runstatus.Run{
		StartedAt: now, FinishedAt: &now, Outcome: runstatus.OutcomeFailed, Reason: "it broke"}, nil)
	if _, reasons = reasonTexts(t, a); !slices.Equal(reasons, []string{"say-hi failed", failing + " (home-joke and say-hi listen to it)"}) {
		t.Fatalf("failing again: %q", reasons)
	}
}

// Another set of permissions (a program's file now declares one) resets
// its health as a change of state does.
func TestDeclaringAnotherPermissionResetsHealth(t *testing.T) {
	fakePermissions(t, map[string]permissions.Status{"screen-recording": permissions.Granted})
	a := testApp(t)
	if err := a.InitTrust(); err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	writeExe(t, src, "prog", "true\n")
	desc := filepath.Join(src, programs.DescriptionFile)
	add := func(file string, update bool) {
		_ = os.WriteFile(desc, []byte(file), 0o600)
		if _, err := programs.Add(a.Layout.Programs(), "snap", desc, update); err != nil {
			t.Fatal(err)
		}
		p, _ := a.Find("snap")
		if err := a.Trust(p); err != nil {
			t.Fatal(err)
		}
	}
	add("executable: ./prog\nwrites: false\n", false)
	e := newEvents(a, func() (*App, error) { return a, nil })
	recordPermissions(a, e, time.Now())
	then := time.Now().Add(-time.Second)
	recordRun(a, "snap", trigger.Occurrence{Trigger: trigger.Manual, At: then}, runstatus.Run{
		StartedAt: then, FinishedAt: &then, Outcome: runstatus.OutcomeFailed, Reason: "the screen was black"}, nil)
	if _, reasons := reasonTexts(t, a); !slices.Equal(reasons, []string{"snap failed"}) {
		t.Fatalf("before: %q", reasons)
	}
	add("executable: ./prog\nwrites: false\npermissions: [screen-recording]\n", true)
	recordPermissions(a, e, time.Now())
	s, reasons := reasonTexts(t, a)
	if len(reasons) != 0 || s.Routines[0].Health != runstatus.HealthNone || s.Routines[0].HealthResetAt == nil {
		t.Fatalf("after: %q %+v", reasons, s.Routines)
	}
}
