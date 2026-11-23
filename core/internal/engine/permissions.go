package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/permissions"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
	"github.com/BorysSerbyn/process-automator/core/internal/sysenv"
	"github.com/BorysSerbyn/process-automator/core/internal/watchers"
)

// Permissions are what the OS must allow before something can work, known
// before its first run. Only what runs code declares one: a plugin (its
// plugin.yaml, its config entry, or the Go plugin), a program, a program
// hook, a watcher. A prompt (an LLM routine, an LLM hook's action) needs
// those of the plugins it uses; a hook, its action's and its watcher's.
//
// macOS answers a check for the process that launched the one checking, so
// only the scheduler, launched from the app, gets the app's answer: it
// checks every permission on each pass and records it (permissions.json),
// and asks macOS for one when `permissions request` asks it to. Everything
// else reads what it recorded; nothing recorded is "not checked", never
// granted. A permission that isn't granted is a readiness problem, and a
// watcher isn't started while one it declares isn't.

// The OS's side, from sysenv (variables for tests).
var (
	checkPermission      = sysenv.CheckPermission
	canRequestPermission = sysenv.CanRequestPermission
	requestPermission    = sysenv.RequestPermission
	permissionPane       = sysenv.PermissionPane
)

// Kinds of what declares a permission (PermissionSource.Kind).
const (
	SourcePlugin  = "plugin"
	SourceProgram = "program"
	SourceHook    = "hook"
	SourceWatcher = "watcher"
)

// PermissionSource is what declares a permission something needs: a plugin
// it uses, its own program (a program's, a program hook's) or its watcher.
type PermissionSource struct {
	Kind string `json:"kind" jsonschema:"enum=plugin,enum=program,enum=hook,enum=watcher"`
	Name string `json:"name"`
}

// PermissionNeed is a permission something needs, what declares it, and
// what the scheduler last found.
type PermissionNeed struct {
	Permission string `json:"permission"`
	// Label is what the OS calls it (Input Monitoring).
	Label string `json:"label"`
	// State is what the scheduler last found: granted, missing, unknown (it
	// can't be checked), unsupported (not on this OS) or not-checked
	// (nothing recorded yet). Only missing and not-checked keep it from
	// running.
	State permissions.Status `json:"state" jsonschema:"enum=granted,enum=missing,enum=unknown,enum=unsupported,enum=not-checked"`
	By    []PermissionSource `json:"by"`
	// Where says where it's turned on, following "turn it on" ("in System
	// Settings > Privacy & Security > Input Monitoring"), and SettingsURL is
	// the address that opens it (absent where there's none).
	Where       string `json:"where,omitempty"`
	SettingsURL string `json:"settingsUrl,omitempty"`
}

// Blocks reports whether it keeps what needs it from running.
func (n PermissionNeed) Blocks() bool { return n.State.Blocks() }

// need is one permission and what declares it, before they're merged.
type need struct {
	permission string
	by         PermissionSource
}

func needsOf(perms []string, by PermissionSource) []need {
	out := make([]need, len(perms))
	for i, p := range perms {
		out[i] = need{p, by}
	}
	return out
}

// ServerPermissions are what the OS must allow a server routines use: its
// config entry's (mcpServers.<name>.permissions, which replaces a preset's)
// or the compiled-in plugin's.
func (a *App) ServerPermissions(server string) []string {
	if perms, ok := a.Config.Permissions[server]; ok {
		return perms
	}
	if p, ok := a.FindPlugin(server); ok {
		return permissions.Sorted(p.Permissions)
	}
	return nil
}

// serverNeeds are what the servers a prompt reaches declare.
func (a *App) serverNeeds(servers []string) []need {
	var out []need
	for _, s := range servers {
		out = append(out, needsOf(a.ServerPermissions(s), PermissionSource{SourcePlugin, s})...)
	}
	return out
}

// watcherNeeds are what a watcher declares.
func watcherNeeds(w watchers.Watcher) []need {
	return needsOf(w.Program.Permissions, PermissionSource{SourceWatcher, w.Name})
}

// permissionNames are the permissions among needs, sorted, each once.
func permissionNames(ns []need) []string {
	var out []string
	for _, n := range ns {
		out = append(out, n.permission)
	}
	return permissions.Sorted(out)
}

// PermissionState is what the scheduler last recorded (nil: nothing yet, or
// it can't be read, which reads the same: not checked).
func (a *App) PermissionState() *permissions.State {
	s, err := permissions.Load(a.Layout.Permissions())
	if err != nil {
		return nil
	}
	return s
}

// merged is needs merged by permission, in permissions.All's order, each
// with what the scheduler recorded.
func (a *App) merged(ns []need) []PermissionNeed {
	if len(ns) == 0 {
		return nil
	}
	state := a.PermissionState()
	var out []PermissionNeed
	for _, info := range permissions.All {
		var by []PermissionSource
		for _, n := range ns {
			if n.permission == info.Name && !slices.Contains(by, n.by) {
				by = append(by, n.by)
			}
		}
		if len(by) > 0 {
			out = append(out, a.permissionNeed(info.Name, state, by))
		}
	}
	return out
}

func (a *App) permissionNeed(name string, state *permissions.State, by []PermissionSource) PermissionNeed {
	where, url := permissionPane(name)
	if by == nil {
		by = []PermissionSource{}
	}
	return PermissionNeed{Permission: name, Label: permissions.Label(name), State: state.Of(name), By: by, Where: where, SettingsURL: url}
}

// Permissions are what x needs before it can work: what the plugins a
// prompt uses declare, a program's own, and a hook's action's and its
// watcher's.
func (a *App) Permissions(x Runnable) []PermissionNeed { return a.merged(x.needs(a)) }

// WatcherPermissions are what a watcher declares.
func (a *App) WatcherPermissions(w watchers.Watcher) []PermissionNeed {
	return a.merged(watcherNeeds(w))
}

// blocked reports whether any of needs keeps what needs it from running.
func blocked(needs []PermissionNeed) bool { return slices.ContainsFunc(needs, PermissionNeed.Blocks) }

// PermissionProblems are needs that keep something (named self) from
// running, as problems: each names the permission, what needs it and
// where it's turned on.
func PermissionProblems(self string, needs []PermissionNeed) []Problem {
	var out []Problem
	for _, n := range needs {
		if !n.Blocks() {
			continue
		}
		who := whoNeeds(self, n.By)
		p := Problem{Permission: n.Permission, Open: n.SettingsURL, Fix: PermissionFix(n),
			Text: fmt.Sprintf("%s needs the %s permission, which isn't allowed for %s", who, n.Label, product.Name)}
		if len(n.By) == 1 && n.By[0].Kind == SourceWatcher && n.By[0].Name != self {
			p.Watcher = n.By[0].Name // only a hook's watcher needs it
		}
		out = append(out, p)
	}
	return out
}

// PermissionFix is how a permission that isn't allowed is given: asking the
// OS from the app, where the OS has a page for it, and turning it on there.
func PermissionFix(n PermissionNeed) string {
	turnOn := "turn it on for " + product.Name
	if n.Where != "" {
		turnOn += " " + n.Where
	}
	if n.SettingsURL == "" {
		return turnOn
	}
	return "`" + product.Command + " permissions request " + n.Permission + "`, or " + turnOn
}

// whoNeeds says what needs a permission, in a problem about self: "it",
// "its plugin notify", "its watcher keys".
func whoNeeds(self string, by []PermissionSource) string {
	var parts []string
	for _, s := range by {
		if s.Name == self && s.Kind != SourcePlugin && s.Kind != SourceWatcher {
			parts = append(parts, "it")
			continue
		}
		parts = append(parts, "its "+s.Kind+" "+s.Name)
	}
	if len(parts) == 0 {
		return "it"
	}
	if slices.Contains(parts, "it") {
		return "it"
	}
	return strings.Join(parts, " and ")
}

// RecordPermissions checks every permission with the OS and records what
// it found: the scheduler does, on each pass (macOS attributes its checks
// to the app). Every permission, not only the ones things declare now, so
// a review of something just added has an answer. With them it records
// what each routine, program, hook and watcher needs, and returns the
// names of those whose needs changed since the last check (a permission's
// state, or which ones): the scheduler resets their health.
func (a *App) RecordPermissions(now time.Time) ([]string, error) {
	found := checkAll()
	declared := a.allNeeds()
	var changed []string
	err := permissions.Update(a.Layout.Permissions(), func(s *permissions.State) {
		s.CheckedAt, s.Permissions = now, found
		needs := map[string]map[string]permissions.Status{}
		for name, perms := range declared {
			needs[name] = map[string]permissions.Status{}
			for _, p := range perms {
				needs[name][p] = s.Of(p)
			}
		}
		changed = permissions.Changed(s.Needs, needs)
		s.Needs = needs
	})
	return changed, err
}

// allNeeds is the permissions each routine, program, hook and watcher
// needs, by name. One that doesn't load is left out.
func (a *App) allNeeds() map[string][]string {
	out := map[string][]string{}
	add := func(name string, ns []need) { out[name] = permissionNames(ns) }
	if all, _, err := a.All(); err == nil {
		for _, x := range all {
			add(x.Name(), x.needs(a))
		}
	}
	if all, _, err := a.Hooks(); err == nil {
		for _, x := range all {
			add(x.Name(), x.needs(a))
		}
	}
	if ws, _, err := a.Watchers(); err == nil {
		for _, w := range ws {
			add(w.Name, watcherNeeds(w))
		}
	}
	return out
}

// recordPermissions records what the OS allows (RecordPermissions) and
// resets the health of what needs a permission whose state changed, or
// whose permissions did: their last runs stop counting toward health (in
// run status) and a watcher's last error is cleared. What's still missing
// stays a problem of its own.
func recordPermissions(a *App, ev *events, now time.Time) {
	changed, err := a.RecordPermissions(now)
	if err != nil {
		logf("permissions: %v", err)
	}
	if len(changed) == 0 {
		return
	}
	logf("permissions: changed for %s; their health starts afresh", strings.Join(changed, ", "))
	if err := runstatus.ResetHealth(a.Layout.RunStatus(), changed, now); err != nil {
		logf("run status: %v", err)
	}
	ev.permissionsChanged(changed)
}

// PermissionsProbeEnv makes the app's binary, when it's set in its environment, print
// what the OS allows it, as JSON, and exit, before anything else runs
// (automator.Main): the scheduler's fresh check on every pass.
const PermissionsProbeEnv = "PA_PERMISSIONS_PROBE"

// probeTimeout is how long one fresh check may take.
const probeTimeout = 20 * time.Second

// PrintPermissionsProbe writes every permission's status, as this process
// finds it, as JSON: what the binary does with PermissionsProbeEnv set.
func PrintPermissionsProbe(w io.Writer) error { return json.NewEncoder(w).Encode(checkHere()) }

// checkHere is every permission's status as this process finds it.
func checkHere() map[string]permissions.Status {
	found := map[string]permissions.Status{}
	for _, name := range permissions.Names() {
		found[name] = checkPermission(name)
	}
	return found
}

// checkAll is every permission's status, checked in a fresh child process
// (this binary, with PermissionsProbeEnv): macOS caches its answers in a
// process that keeps running, so the scheduler asking itself again would
// keep saying what it found first, granted after I've taken a permission
// away. A child of the scheduler is attributed to the app as a watcher or a
// plugin's server is, so its answer is theirs. When the child can't answer,
// the check is this process's own. A variable for tests.
var checkAll = checkInChild

func checkInChild() map[string]permissions.Status {
	exe, err := os.Executable()
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, exe)
		cmd.Env = append(os.Environ(), PermissionsProbeEnv+"=1")
		var out []byte
		if out, err = cmd.Output(); err == nil {
			var found map[string]permissions.Status
			if err = json.Unmarshal(out, &found); err == nil && len(found) == len(permissions.Names()) {
				return found
			}
			if err == nil {
				err = fmt.Errorf("it answered for %d permissions, not %d", len(found), len(permissions.Names()))
			}
		}
	}
	logf("permissions: a fresh check failed (%v): checking in the scheduler itself", err)
	return checkHere()
}

// PermissionRequest is how asking the OS for a permission went
// (`permissions request`).
type PermissionRequest struct {
	PermissionNeed
	// Asked is whether the scheduler asked the OS (the first time, macOS
	// shows its prompt naming the app; after that only its settings change
	// it); Why says why not.
	Asked bool   `json:"asked"`
	Why   string `json:"why,omitempty"`
}

// Permission request timings.
const (
	// requestPoll is how often the scheduler looks for requests.
	requestPoll = 500 * time.Millisecond
	// requestTake is how long a request waits for the scheduler to take it.
	requestTake = 10 * time.Second
	// requestSettle is how long it then waits for the OS's answer to be
	// recorded (one shown in a prompt takes as long as I do).
	requestSettle = 3 * time.Second
)

// RequestPermission asks the scheduler to ask the OS for a permission: it's
// the process macOS attributes to the app, so the prompt names the app
// (a command typed in a terminal would ask for the terminal). It waits for
// the scheduler to take it and, briefly, for the answer, then says how it
// went and where the permission is turned on.
func (a *App) RequestPermission(ctx context.Context, name string, now time.Time) (PermissionRequest, error) {
	if !permissions.Known(name) {
		return PermissionRequest{}, fmt.Errorf("unknown permission %q (one of: %s)", name, strings.Join(permissions.Names(), ", "))
	}
	out := PermissionRequest{PermissionNeed: a.permissionNeed(name, a.PermissionState(), nil)}
	if st, err := runstatus.Load(a.Layout.RunStatus()); err != nil || st.ValidUntil == nil || now.After(*st.ValidUntil) {
		out.Why = "the scheduler isn't running, and only it asks as " + product.Name + ": `" + product.Command + " login-items start` (opening " + product.Name + " runs it too)"
		return out, nil
	}
	dir := a.Layout.PermissionRequests()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return out, err
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(now.Format(time.RFC3339)+"\n"), 0o600); err != nil {
		return out, err
	}
	taken := waitFor(ctx, requestTake, func() bool {
		_, err := os.Stat(path)
		return errors.Is(err, os.ErrNotExist)
	})
	if !taken {
		_ = os.Remove(path)
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		out.Why = "the scheduler didn't take the request: `" + product.Command + " doctor` says why"
		return out, nil
	}
	// Its answer: whether it asked, then (briefly) what the OS says now.
	var req permissions.Request
	waitFor(ctx, requestSettle, func() bool {
		s := a.PermissionState()
		if s == nil {
			return false
		}
		r, ok := s.Requests[name]
		if !ok || r.At.Before(now.Truncate(time.Second)) {
			return false
		}
		req = r
		return !r.Asked || s.Of(name) == permissions.Granted
	})
	out.Asked, out.Why = req.Asked, req.Why
	out.State = a.PermissionState().Of(name)
	return out, nil
}

// waitFor polls done until it's true (true), or d passes or ctx ends (false).
func waitFor(ctx context.Context, d time.Duration, done func() bool) bool {
	deadline := time.Now().Add(d)
	for {
		if done() {
			return true
		}
		if time.Now().After(deadline) || !sleepCtx(ctx, 100*time.Millisecond) {
			return done()
		}
	}
}

// servePermissionRequests takes requests to ask the OS for a permission
// (RequestPermission) until ctx ends: it asks, from the daemon, and records
// what the OS says once it has answered, with run status brought up to date
// and a pass at once. Meanwhile f follows the permission, for one turned on
// in the OS's settings.
func servePermissionRequests(ctx context.Context, a *App, ev *events, reload func() (*App, error), f *follower) {
	t := time.NewTicker(requestPoll)
	defer t.Stop()
	dir := a.Layout.PermissionRequests()
	var mu sync.Mutex
	asking := map[string]bool{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // none yet
		}
		for _, e := range entries {
			name := e.Name()
			if err := os.Remove(filepath.Join(dir, name)); err != nil || !permissions.Known(name) {
				continue
			}
			ok, why := canRequestPermission(name)
			mu.Lock()
			already := asking[name]
			asking[name] = asking[name] || ok
			mu.Unlock()
			if err := permissions.Update(a.Layout.Permissions(), func(s *permissions.State) {
				if s.Requests == nil {
					s.Requests = map[string]permissions.Request{}
				}
				s.Requests[name] = permissions.Request{At: time.Now(), Asked: ok, Why: why}
			}); err != nil {
				logf("permissions: %v", err)
			}
			// Turned on in the OS's settings, where a request often sends
			// me, it's acted on at once.
			go f.follow(ctx, a, name)
			if !ok {
				logf("permissions: can't ask the OS for %s: %s", name, why)
				continue
			}
			if already {
				continue // its prompt is up
			}
			logf("permissions: asking the OS for %s", name)
			go func() {
				requestPermission(name) // may wait for my answer
				mu.Lock()
				delete(asking, name)
				mu.Unlock()
				cur, err := reload()
				if err != nil {
					logf("permissions: %v", err)
					return
				}
				recordPermissions(cur, ev, time.Now())
				logf("permissions: %s is %s", name, cur.PermissionState().Of(name))
				if err := cur.SyncStatus(time.Now()); err != nil {
					logf("run status: %v", err)
				}
				f.wake.poke() // a pass acts on it: the watchers it lets run start
			}()
		}
	}
}

// Kinds of what needs a permission (PermissionUse.Kind).
const (
	UseRoutine = "routine"
	UseProgram = "program"
	UseHook    = "hook"
	UseWatcher = "watcher"
)

// PermissionUse is something that needs a permission: a routine (LLM), a
// program, a hook or a watcher.
type PermissionUse struct {
	Kind string `json:"kind" jsonschema:"enum=routine,enum=program,enum=hook,enum=watcher"`
	Name string `json:"name"`
	// Enabled is whether it's on: a routine or a hook enabled, a watcher not
	// paused.
	Enabled bool `json:"enabled"`
	// By is what declares the permission for it.
	By []PermissionSource `json:"by"`
}

// PermissionStatus is a permission, what the scheduler last found, and
// everything that needs it (By: what declares it for any of them).
type PermissionStatus struct {
	PermissionNeed
	NeededBy []PermissionUse `json:"neededBy"`
}

// PermissionReport is what's needed, by what, and what the scheduler last
// found (`permissions list`).
type PermissionReport struct {
	// CheckedAt is when the scheduler last checked (absent: never, so every
	// state is not-checked).
	CheckedAt   *time.Time         `json:"checkedAt,omitempty"`
	Permissions []PermissionStatus `json:"permissions"`
}

// PermissionReport is every permission something installed needs (every
// permission, with all), with what needs it and what the scheduler found.
func (a *App) PermissionReport(all bool) (PermissionReport, error) {
	uses := map[string][]PermissionUse{}
	add := func(kind, name string, enabled bool, ns []need) {
		for _, n := range a.merged(ns) {
			uses[n.Permission] = append(uses[n.Permission], PermissionUse{Kind: kind, Name: name, Enabled: enabled, By: n.By})
		}
	}
	routines, _, err := a.All()
	if err != nil {
		return PermissionReport{}, err
	}
	for _, x := range routines {
		c, _ := x.runConfig(a)
		kind := UseRoutine
		if x.Kind() == KindProgram {
			kind = UseProgram
		}
		add(kind, x.Name(), c.Enabled, x.needs(a))
	}
	hooks, _, err := a.Hooks()
	if err != nil {
		return PermissionReport{}, err
	}
	for _, x := range hooks {
		c, _ := x.runConfig(a)
		add(UseHook, x.Name(), c.Enabled, x.needs(a))
	}
	ws, _, err := a.Watchers()
	if err != nil {
		return PermissionReport{}, err
	}
	for _, w := range ws {
		on, _ := a.WatcherEnabled(w)
		add(UseWatcher, w.Name, on, watcherNeeds(w))
	}
	state := a.PermissionState()
	out := PermissionReport{Permissions: []PermissionStatus{}}
	if state != nil {
		at := state.CheckedAt
		out.CheckedAt = &at
	}
	for _, info := range permissions.All {
		used := uses[info.Name]
		if len(used) == 0 && !all {
			continue
		}
		var by []PermissionSource
		for _, u := range used {
			for _, s := range u.By {
				if !slices.Contains(by, s) {
					by = append(by, s)
				}
			}
		}
		if used == nil {
			used = []PermissionUse{}
		}
		out.Permissions = append(out.Permissions, PermissionStatus{PermissionNeed: a.permissionNeed(info.Name, state, by), NeededBy: used})
	}
	return out, nil
}
