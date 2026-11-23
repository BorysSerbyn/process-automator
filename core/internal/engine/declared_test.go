//go:build unix

package engine

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/hooks"
	"github.com/BorysSerbyn/process-automator/core/internal/watchers"
)

// navKeys declares two events.
const navKeys = "executable: ./watch\nmode: stream\nevents:\n  key.home: Home key pressed\n  key.end: End key pressed\n"

// A hook picks its type from the events its watcher declares: what it
// listens for comes with its label, another type is refused (and, after
// the watcher changes, a problem), and the watcher's events say which
// hooks act on each. A watcher declaring none takes any type, with a hint.
func TestAHookPicksFromItsWatchersEvents(t *testing.T) {
	a := testApp(t)
	w := addWatcher(t, a, "nav-keys", navKeys, "sleep 60\n")
	addHook(t, a, "on-end", hooks.DescriptionFile, "executable: ./act\nwrites: false\non: {watcher: nav-keys, type: key.end}\n", "true\n")
	addHook(t, a, "on-any", hooks.DescriptionFile, "executable: ./act\nwrites: false\non: {watcher: nav-keys}\n", "true\n")

	on := a.LabeledOn(hooks.On{Watcher: "nav-keys", Type: "key.end"})
	if on.Label != "End key pressed" || on.When() != "When End key pressed (from nav-keys)" {
		t.Fatalf("%+v %q", on, on.When())
	}
	if got := a.LabeledOn(hooks.On{Watcher: "nav-keys"}).When(); got != "When any event (from nav-keys)" {
		t.Fatalf("every type: %q", got)
	}
	err := a.CheckOn(hooks.On{Watcher: "nav-keys", Type: "key.pgup"})
	if err == nil || !strings.Contains(err.Error(), "key.home (Home key pressed), key.end (End key pressed)") {
		t.Fatalf("an undeclared type: %v", err)
	}
	if a.CheckOn(hooks.On{Watcher: "nav-keys", Type: "key.end"}) != nil || a.CheckOn(hooks.On{Watcher: "nowhere", Type: "x"}) != nil {
		t.Fatal("a declared type, or a missing watcher, refused")
	}
	all, _, _ := a.Hooks()
	evs := WatcherEvents(w, all)
	if len(evs) != 2 || evs[0].Type != "key.home" || !slices.Equal(evs[0].Hooks, []string{"on-any"}) ||
		evs[1].Label != "End key pressed" || !slices.Equal(evs[1].Hooks, []string{"on-any", "on-end"}) {
		t.Fatalf("%+v", evs)
	}
	if WatcherHint(w) != "" {
		t.Fatal("a hint for a watcher that declares its events")
	}

	// A sample fired by hand is one the watcher could print.
	x, _ := a.FindHook("on-end")
	if _, err := a.Fire(context.Background(), x, watchers.Event{ID: "1", Type: "key.pgup", At: time.Now()}, RunOptions{DryRun: true}); err == nil ||
		!strings.Contains(err.Error(), "doesn't print key.pgup events") {
		t.Fatalf("fired an undeclared type: %v", err)
	}

	// The watcher drops key.end: the hook listening for it has a problem,
	// and the change takes the watcher's trust away, saying what changed.
	src := t.TempDir()
	writeExe(t, src, "watch", "sleep 60\n")
	desc := filepath.Join(src, watchers.DescriptionFile)
	if err := os.WriteFile(desc, []byte("executable: ./watch\nmode: stream\nevents:\n  key.home: Home pressed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := watchers.Add(a.Layout.Watchers(), "nav-keys", desc, true); err != nil {
		t.Fatal(err)
	}
	rd := a.Assess(x)
	if len(rd.Problems) == 0 || !strings.Contains(rd.Problems[0].Text, "it listens for key.end, which its watcher nav-keys doesn't print") {
		t.Fatalf("problems %+v", rd.Problems)
	}
	review := a.HookWatcherReview("nav-keys")
	if review.Trusted || !slices.Contains(review.Changes, "no longer declares key.end events (End key pressed)") ||
		!slices.Contains(review.Changes, `calls key.home "Home pressed", not "Home key pressed"`) {
		t.Fatalf("changes %v", review.Changes)
	}
}

// A watcher written before watchers declared their events works as before:
// any type reaches its hooks, nothing's a problem, and a hint says to
// declare them.
func TestAWatcherDeclaringNoEvents(t *testing.T) {
	a := testApp(t)
	w := addWatcher(t, a, "cal", "executable: ./watch\nmode: poll\ninterval: 5s\n",
		"echo '{\"id\":\"e1\",\"type\":\"meeting.started\"}'\necho '{\"cursor\":\"c\"}'\n")
	addHook(t, a, "open-meet", hooks.DescriptionFile, recordingHook, "cat >> got.jsonl\n")
	if WatcherHint(w) == "" || a.CheckOn(hooks.On{Watcher: "cal", Type: "anything"}) != nil || len(WatcherEvents(w, nil)) != 0 {
		t.Fatal("a watcher declaring no events")
	}
	if on := a.LabeledOn(hooks.On{Watcher: "cal", Type: "meeting.started"}); on.Label != "" || on.When() != "When a meeting.started event (from cal)" {
		t.Fatalf("%+v", on)
	}
	x, _ := a.FindHook("open-meet")
	if rd := a.Assess(x); len(rd.All()) != 0 {
		t.Fatalf("problems %+v", rd.All())
	}
	_, stop := startEvents(t, a)
	waitUntil(t, "its event", func() bool { return len(gotEvents(a, "open-meet")) == 1 })
	stop()
}
