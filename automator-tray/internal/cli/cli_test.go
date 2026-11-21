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
		"status.schema.json":             RunStatus{},
		"notifications-list.schema.json": []Notification{},
		"notifications-open.schema.json": ClickOutput{},
		"update.schema.json":             UpdateOutput{},
		"update-progress.schema.json":    UpdateProgress{},
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
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		prop, ok := props[name].(map[string]any)
		if !ok {
			t.Errorf("%s: no property %q (%s.%s)", where, name, typ.Name(), f.Name)
			continue
		}
		checkFields(t, where+"."+name, f.Type, prop)
	}
}
