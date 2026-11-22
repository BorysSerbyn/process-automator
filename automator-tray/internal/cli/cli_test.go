package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// fake writes a stand-in for the app's CLI: a shell script running body.
func fake(t *testing.T, body string) CLI {
	t.Helper()
	path := filepath.Join(t.TempDir(), "acme-auto")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return CLI{Path: path, Command: "acme-auto"}
}

// A failure is the core's error object, else its stderr without "Error: ".
func TestFailuresAreTheCoresWords(t *testing.T) {
	c := fake(t, `case $1 in
  json) echo '{"error": "no such run"}'; echo "Error: no such run" >&2; exit 1 ;;
  text) echo "Error: config doesn't load" >&2; exit 1 ;;
  *) exit 3 ;;
esac`)
	for arg, want := range map[string]string{"json": "no such run", "text": "config doesn't load", "silent": "acme-auto failed"} {
		if _, err := c.Exec(arg); err == nil || err.Error() != want {
			t.Errorf("%s: got %v, want %q", arg, err, want)
		}
	}
	if _, err := (CLI{Path: "/nonexistent/acme-auto", Command: "acme-auto"}).Exec(); err == nil ||
		err.Error() != "couldn't start acme-auto" {
		t.Errorf("missing CLI: %v", err)
	}
}

// A command that hangs is given up on, saying so, so whatever waits for it
// (the next notifications, the next update check) isn't held up for good.
func TestAHungCommandTimesOut(t *testing.T) {
	orig := Timeout
	Timeout = 200 * time.Millisecond
	t.Cleanup(func() { Timeout = orig })
	c := fake(t, "exec sleep 30\n")
	start := time.Now()
	_, err := c.PendingNotifications()
	if err == nil || !strings.Contains(err.Error(), "didn't answer") {
		t.Errorf("err = %v", err)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("waited %s", took)
	}
}

func TestCallsRunTheCommandsAutomatorUIRuns(t *testing.T) {
	log := filepath.Join(t.TempDir(), "log")
	c := fake(t, `echo "$*" >> '`+log+`'
case "$*" in
  "update --check --json") echo '{"current":"1.0.0","latest":"v1.1.0","updateAvailable":true,"updated":false,"inProgress":false,"note":"It restarts."}' ;;
  "notifications list --pending --json") echo '[{"id":"n1","at":"2026-11-17T08:00:00Z","kind":"failure","title":"standup failed","body":"why","routine":"standup","run":"r1"}]' ;;
  *) echo '{}' ;;
esac`)
	u, err := c.UpdateCheck()
	if err != nil || u != (UpdateOutput{Current: "1.0.0", Latest: "v1.1.0", UpdateAvailable: true, Note: "It restarts."}) {
		t.Fatalf("%+v %v", u, err)
	}
	n, err := c.PendingNotifications()
	if err != nil || !reflect.DeepEqual(n, []Notification{{ID: "n1", Title: "standup failed", Body: "why"}}) {
		t.Fatalf("%+v %v", n, err)
	}
	_ = c.MarkDelivered([]string{"n1", "n2"})
	_ = c.OpenRun("r1")
	_, _ = c.OpenNotification("n1")
	_, _ = c.StartUpdate()
	_, _ = c.UpdateProgress()
	b, _ := os.ReadFile(log)
	want := "update --check --json\nnotifications list --pending --json\nnotifications delivered n1 n2\n" +
		"runs show r1 --open\nnotifications open n1 --json\nupdate --detach --json\nupdate progress --json\n"
	if string(b) != want {
		t.Fatalf("ran:\n%s\nwant:\n%s", b, want)
	}
}

// The windows' calls run the commands automator-ui's Setup and Settings
// windows run.
func TestWindowCallsRunTheCommandsAutomatorUIRuns(t *testing.T) {
	log := filepath.Join(t.TempDir(), "log")
	c := fake(t, `echo "$*" >> '`+log+`'
case "$1" in
  settings|templates) [ "$2" = list ] && echo '[]' || echo '{}' ;;
  *) echo '{}' ;;
esac`)
	_, _ = c.Setup()
	_, _ = c.Templates()
	_ = c.InstallTemplate("projects/acme/standup", []Value{{"channel", "#team"}, {"when", "9:00"}})
	_, _ = c.AppSettings()
	_, _ = c.SetSetting("llm.backend", "local")
	_, _ = c.ResetSetting("llm.local.url")
	_ = c.SignIn("tracker")
	_, _ = c.TrustCheck("standup")
	_ = c.TurnOn("standup")
	_ = c.ConnectClaudeCode()
	_ = c.InstallStatusLine()
	_, _ = c.Usage()
	_, _ = c.LoginItems()
	_, _ = c.SetStartAtLogin(true)
	_, _ = c.SetStartAtLogin(false)
	b, _ := os.ReadFile(log)
	want := strings.Join([]string{
		"setup --json", "templates list --json",
		"templates install projects/acme/standup --set channel=#team --set when=9:00 --json",
		"settings list --scope app --json", "settings set llm.backend local --json", "settings reset llm.local.url --json",
		"auth login tracker --browser", "routines trust standup --check --json", "routines trust standup --yes --resume --json",
		"mcp connect --json", "usage statusline install --json", "usage --json",
		"login-items status --json", "login-items enable --json", "login-items disable --json",
	}, "\n") + "\n"
	if string(b) != want {
		t.Fatalf("ran:\n%s\nwant:\n%s", b, want)
	}
}

// The hook and watcher windows' calls run the commands automator-ui's run;
// firing a hook waits for its run, past Timeout.
func TestHookCallsRunTheCommandsAutomatorUIRuns(t *testing.T) {
	orig := Timeout
	Timeout = 2 * time.Second
	t.Cleanup(func() { Timeout = orig })
	log := filepath.Join(t.TempDir(), "log")
	c := fake(t, `echo "$*" >> '`+log+`'
case "$1 $2" in
  "hooks list"|"watchers list") echo '[]' ;;
  "hooks fire") sleep 3; echo '{"kind":"program","program":{"exitCode":0,"output":"ok","dryRun":true}}' ;;
  *) echo '{}' ;;
esac`)
	_, _ = c.Hooks()
	_, _ = c.Hook("open-meet")
	_, _ = c.HookTrustCheck("open-meet")
	_ = c.TrustHook("open-meet", false)
	_ = c.TrustHook("open-meet", true)
	_ = c.SetHookPaused("open-meet", true)
	_ = c.SetHookPaused("open-meet", false)
	_ = c.DeleteHook("open-meet")
	fired, err := c.FireHook("open-meet", `{"id":"1","type":"meeting.started"}`, true)
	if err != nil || fired.Program == nil || !fired.Program.DryRun {
		t.Fatalf("fire: %+v %v", fired, err)
	}
	_, _ = c.Watchers()
	_, _ = c.Watcher("cal")
	_, _ = c.WatcherTrustCheck("cal")
	_ = c.TrustWatcher("cal")
	_ = c.SetWatcherPaused("cal", true)
	_ = c.SetWatcherPaused("cal", false)
	_ = c.DeleteWatcher("cal")
	b, _ := os.ReadFile(log)
	want := strings.Join([]string{
		"hooks list --json", "hooks show open-meet --json", "hooks trust open-meet --check --json",
		"hooks trust open-meet --yes --json", "hooks trust open-meet --yes --resume --json",
		"hooks pause open-meet --json", "hooks resume open-meet --json", "hooks delete open-meet --json",
		`hooks fire open-meet --event-json {"id":"1","type":"meeting.started"} --dry-run --json`,
		"watchers list --json", "watchers show cal --json", "watchers trust cal --check --json",
		"watchers trust cal --yes --json", "watchers pause cal --json", "watchers resume cal --json",
		"watchers delete cal --json",
	}, "\n") + "\n"
	if string(b) != want {
		t.Fatalf("ran:\n%s\nwant:\n%s", b, want)
	}
}

// Run status keeps hooks apart from routines; a reason or a click about a
// hook says so; a hook's run has its events, whose data is plain text.
func TestHooksInWhatTheCoreSays(t *testing.T) {
	s := ParseStatus([]byte(`{"schemaVersion":1,"updatedAt":"2026-11-22T14:55:10Z","health":"error","busy":false,
		"routines":[{"name":"standup","enabled":true}],
		"hooks":[{"name":"open-meet","enabled":true,"health":"error","summary":"open-meet can't run"}],
		"reasons":[{"routine":"open-meet","hook":true,"text":"open-meet can't run"}]}`))
	if s == nil || s.Routine("open-meet") != nil || s.Hook("open-meet") == nil || s.Hook("standup") != nil ||
		!s.Reasons[0].Hook || s.Reasons[0].Routine != "open-meet" {
		t.Fatalf("%+v", s)
	}
	var e RunEntry
	if err := json.Unmarshal([]byte(`{"id":"open-meet/x","hook":true,"outcome":"success","events":[
		{"id":"m1","type":"meeting.started","at":"2026-11-22T09:30:00Z","watcher":"cal","data":{"title":"<b>Standup</b>"}},
		{"id":"m2","type":"meeting.started","at":"2026-11-22T09:31:00Z"}]}`), &e); err != nil {
		t.Fatal(err)
	}
	if !e.Hook || len(e.Events) != 2 || e.Events[0].DataText() != "{\n  \"title\": \"<b>Standup</b>\"\n}" || e.Events[1].DataText() != "" {
		t.Fatalf("%+v %q", e, e.Events[0].DataText())
	}
}

// Signing in waits for the browser as long as it takes: Timeout doesn't
// stop it.
func TestSigningInHasNoTimeLimit(t *testing.T) {
	orig := Timeout
	Timeout = 100 * time.Millisecond
	t.Cleanup(func() { Timeout = orig })
	c := fake(t, "sleep 0.5\n")
	if err := c.SignIn("tracker"); err != nil {
		t.Fatalf("sign-in stopped: %v", err)
	}
	if _, err := c.Setup(); err == nil || !strings.Contains(err.Error(), "didn't answer") {
		t.Fatalf("setup wasn't stopped: %v", err)
	}
}

func TestParseStatus(t *testing.T) {
	for line, ok := range map[string]bool{
		`{"schemaVersion":1,"updatedAt":"2026-11-17T08:05:00Z","health":"ok","busy":false,"routines":[]}`:    true,
		`{"schemaVersion":2,"updatedAt":"2026-11-17T08:05:00Z","health":"ok","busy":false,"routines":[]}`:    false,
		`{"schemaVersion":1,"updatedAt":"2026-11-17T08:05:00Z","health":"great","busy":false,"routines":[]}`: false,
		`not json`: false,
	} {
		if got := ParseStatus([]byte(line)) != nil; got != ok {
			t.Errorf("%s: read %v", line, got)
		}
	}
}

// Follow passes on each line, and starts the watch again when it ends.
func TestFollowRestartsTheWatch(t *testing.T) {
	saved := RestartDelay
	RestartDelay = 10 * time.Millisecond
	t.Cleanup(func() { RestartDelay = saved })
	c := fake(t, `[ "$*" = "status --watch" ] || exit 2
echo '{"schemaVersion":1,"updatedAt":"2026-11-17T08:05:00Z","health":"ok","busy":true,"routines":[{"name":"a","enabled":true}]}'
echo 'garbage'
`)
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	var got []*RunStatus
	done := make(chan struct{})
	go func() {
		c.Follow(ctx, func(s *RunStatus) {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, s)
			if len(got) == 4 {
				cancel()
			}
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Follow didn't restart the watch")
	}
	mu.Lock()
	defer mu.Unlock()
	if got[0] == nil || !got[0].Busy || got[0].Routines[0].Name != "a" || got[1] != nil || got[2] == nil {
		t.Fatalf("got %+v", got)
	}
}

// The tray's types read the core's JSON by the names contracts/ gives it:
// each field is a property of the command's output schema.
func TestTypesMatchTheContracts(t *testing.T) {
	for schema, v := range map[string]any{
		"status.schema.json":              RunStatus{},
		"notifications-list.schema.json":  []Notification{},
		"notifications-open.schema.json":  ClickOutput{},
		"update.schema.json":              UpdateOutput{},
		"update-progress.schema.json":     UpdateProgress{},
		"setup.schema.json":               SetupOutput{},
		"templates-list.schema.json":      []TemplateInfo{},
		"settings-list.schema.json":       []Setting{},
		"settings-set.schema.json":        Setting{},
		"settings-reset.schema.json":      Setting{},
		"routines-trust.schema.json":      TrustOutput{},
		"usage.schema.json":               Report{},
		"login-items-status.schema.json":  LoginItems{},
		"login-items-enable.schema.json":  LoginItems{},
		"login-items-start.schema.json":   RunningOutput{},
		"login-items-stop.schema.json":    RunningOutput{},
		"paths.schema.json":               Paths{},
		"routines-list.schema.json":       []RoutineInfo{},
		"routines-show.schema.json":       RoutineDetail{},
		"runs.schema.json":                []RunSummary{},
		"runs-show.schema.json":           RunEntry{},
		"memory-list.schema.json":         []Memory{},
		"hooks-list.schema.json":          []HookInfo{},
		"hooks-show.schema.json":          HookDetail{},
		"hooks-trust.schema.json":         TrustOutput{},
		"hooks-fire.schema.json":          FireOutput{},
		"watchers-list.schema.json":       []WatcherInfo{},
		"watchers-show.schema.json":       WatcherDetail{},
		"watchers-trust.schema.json":      WatcherTrustOutput{},
		"permissions-request.schema.json": PermissionRequest{},
	} {
		b, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "cli", schema))
		if err != nil {
			t.Fatal(err)
		}
		var node map[string]any
		if err := json.Unmarshal(b, &node); err != nil {
			t.Fatal(err)
		}
		checkFields(t, schema, reflect.TypeOf(v), node)
	}
}

func checkFields(t *testing.T, where string, typ reflect.Type, node map[string]any) {
	t.Helper()
	for typ.Kind() == reflect.Slice || typ.Kind() == reflect.Pointer {
		if typ.Kind() == reflect.Slice {
			node, _ = node["items"].(map[string]any)
		}
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct || typ == reflect.TypeOf(time.Time{}) {
		return
	}
	props, _ := node["properties"].(map[string]any)
	for i := range typ.NumField() {
		f := typ.Field(i)
		if f.Anonymous {
			checkFields(t, where, f.Type, node) // its fields are the struct's own
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		prop, ok := props[name].(map[string]any)
		if !ok {
			t.Errorf("%s: no property %q (%s.%s)", where, name, typ.Name(), f.Name)
			continue
		}
		checkFields(t, where+"."+name, f.Type, prop)
	}
}

// Grant… asks through `permissions request`; a permission's words come
// from what the core says, and unknown or unsupported ones never block.
func TestPermissions(t *testing.T) {
	log := filepath.Join(t.TempDir(), "log")
	c := fake(t, `echo "$*" >> '`+log+`'
echo '{"permission":"input-monitoring","label":"Input Monitoring","state":"missing","asked":false,"by":[],"where":"in System Settings > Privacy & Security > Input Monitoring"}'`)
	r, err := c.RequestPermission("input-monitoring")
	if err != nil || r.Instructions("Acme") != "Switch on Acme in System Settings > Privacy & Security > Input Monitoring." {
		t.Fatalf("%+v %v", r, err)
	}
	if b, _ := os.ReadFile(log); string(b) != "permissions request input-monitoring --json\n" {
		t.Fatalf("ran %q", b)
	}
	if (PermissionRequest{State: "granted"}).Instructions("Acme") != "" {
		t.Error("instructions for a granted permission")
	}
	list := []PermissionNeed{
		{Label: "Input Monitoring", State: "missing", By: []PermissionSource{{Kind: "watcher", Name: "home-key"}}},
		{Label: "Notifications", State: "not-checked", By: []PermissionSource{{Kind: "plugin", Name: "notify"}}},
		{Label: "Full Disk Access", State: "unknown"},
		{Label: "Camera", State: "unsupported"},
		{Label: "Accessibility", State: "granted", By: []PermissionSource{{Kind: "program", Name: "x"}}},
	}
	if got := WontRunText(list); got != "It won't run until Input Monitoring and Notifications are allowed." {
		t.Errorf("%q", got)
	}
	if list[0].Reason() != "for its watcher home-key" || list[1].Reason() != "for the notify plugin" || list[4].Reason() != "for its program" {
		t.Error("reasons")
	}
	if list[2].Blocking() || list[3].Blocking() || list[2].Shown() || list[3].Shown() || !list[4].Shown() {
		t.Error("unknown or unsupported blocks or shows")
	}
}
