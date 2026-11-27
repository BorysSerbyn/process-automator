package tray

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

const pending = `[
 {"id":"n1","at":"2026-11-17T08:00:00Z","kind":"failure","title":"standup failed","body":"tracker: token expired","routine":"standup","run":"r-42"},
 {"id":"n2","at":"2026-11-17T08:01:00Z","kind":"app","title":"Updated to v2.2.0"}
]`

// Pending notifications are posted, each with the app's own title and body
// and a click (default action), and marked delivered.
func TestPendingNotificationsAreDeliveredOnce(t *testing.T) {
	f := newFakeCLI(t)
	f.answer("notifications list --pending --json", pending)
	tr, _, n, _ := newTray(t, f)
	tr.notifier.deliver()

	want := map[uint32]Note{
		1: {Summary: "standup failed", Body: "tracker: token expired", Actions: []string{"default", "Open"}},
		2: {Summary: "Updated to v2.2.0", Actions: []string{"default", "Open"}},
	}
	if !reflect.DeepEqual(n.notes, want) {
		t.Fatalf("posted %+v", n.notes)
	}
	if !f.hasRun("notifications delivered n1 n2") {
		t.Fatalf("not marked delivered: %q", f.ran())
	}
	// Still pending (the core didn't take the mark): not posted again.
	tr.notifier.deliver()
	if n.count() != 2 {
		t.Fatalf("posted again: %+v", n.notes)
	}
}

// What was shown is forgotten once the core no longer lists it as pending:
// a long-running tray doesn't keep every notification it ever showed.
func TestShownNotificationsAreForgottenOnceDelivered(t *testing.T) {
	f := newFakeCLI(t)
	f.answer("notifications list --pending --json", pending)
	tr, _, _, _ := newTray(t, f)
	tr.notifier.deliver()
	f.answer("notifications list --pending --json", "[]")
	tr.notifier.deliver()
	tr.notifier.mu.Lock()
	defer tr.notifier.mu.Unlock()
	if len(tr.notifier.posted) != 0 {
		t.Fatalf("still remembered: %v", tr.notifier.posted)
	}
}

// A note the desktop doesn't take stays pending, and is posted next time.
func TestUnshownNotificationsStayPending(t *testing.T) {
	f := newFakeCLI(t)
	f.answer("notifications list --pending --json", pending)
	tr, _, n, _ := newTray(t, f)
	n.refuse["standup failed"] = true
	tr.notifier.deliver()
	if !f.hasRun("notifications delivered n2") || f.hasRun("notifications delivered n1 n2") {
		t.Fatalf("marked: %q", f.ran())
	}
	delete(n.refuse, "standup failed")
	tr.notifier.deliver()
	if n.find("standup failed") == 0 || !f.hasRun("notifications delivered n1") {
		t.Fatalf("not posted again: %+v %q", n.notes, f.ran())
	}
}

// When the core can't be asked, nothing is posted or marked.
func TestNoDeliveryWhenTheCoreFails(t *testing.T) {
	f := newFakeCLI(t)
	f.fail("notifications list --pending --json", "config doesn't load")
	tr, _, n, _ := newTray(t, f)
	tr.notifier.deliver()
	if n.count() != 0 || len(f.ran()) != 1 {
		t.Fatalf("%+v %q", n.notes, f.ran())
	}
}

// A click asks the core what to show: a run opens in its routine's screen
// on that run (the window seam), an update's Updates, setup's Setup. Other
// actions do nothing.
func TestAClickShowsWhatTheCoreSays(t *testing.T) {
	f := newFakeCLI(t)
	f.answer("notifications list --pending --json", strings.TrimSuffix(pending, "\n]")+`,
 {"id":"n3","at":"2026-11-17T08:02:00Z","kind":"app","setup":true,"title":"Finish setting up","body":"Still to do: Model backend"}
]`)
	f.answer("notifications open n1 --json", `{"notification":"n1","action":"show-run","routine":"standup","run":"r-42"}`)
	f.answer("notifications open n2 --json", `{"notification":"n2","action":"show-updates"}`)
	f.answer("notifications open n3 --json", `{"notification":"n3","action":"show-setup"}`)
	tr, _, n, w := newTray(t, f)
	tr.notifier.deliver()

	tr.Desk().Invoked(n.find("standup failed"), "default")
	eventually(t, "the routine on its run", func() bool {
		return w.has(Window{Kind: RoutineWindow, Routine: "standup", Run: "r-42"})
	})
	if !f.hasRun("notifications open n1 --json") {
		t.Fatalf("%q", f.ran())
	}
	tr.Desk().Invoked(n.find("Updated to v2.2.0"), "default")
	eventually(t, "the Manage Updates window", func() bool { return w.has(Window{Kind: UpdatesWindow}) })
	tr.Desk().Invoked(n.find("Finish setting up"), "default")
	eventually(t, "the Setup window", func() bool { return w.has(Window{Kind: SetupWindow}) })

	before := len(f.ran())
	tr.Desk().Invoked(n.find("standup failed"), "dismissed")
	tr.Desk().Invoked(999, "default") // not one of the tray's
	tr.Desk().Closed(n.find("standup failed"))
	tr.Desk().Invoked(n.find("standup failed"), "default") // gone
	if len(f.ran()) != before {
		t.Fatalf("ran %q", f.ran()[before:])
	}
}

// A click the core can't follow says why, in its words.
func TestAClickThatFailsSaysWhy(t *testing.T) {
	f := newFakeCLI(t)
	f.answer("notifications list --pending --json", pending)
	f.fail("notifications open n1 --json", "no notification n1")
	tr, _, n, _ := newTray(t, f)
	tr.notifier.deliver()
	tr.Desk().Invoked(n.find("standup failed"), "default")
	eventually(t, "the alert", func() bool { return n.find("Couldn't open the notification") != 0 })
	if got := n.note(n.find("Couldn't open the notification")); got.Body != "no notification n1" || got.Actions != nil {
		t.Fatalf("%+v", got)
	}
}

// One a routine asked me to acknowledge is posted with Acknowledge, stays,
// and breaks through when urgent; Acknowledge runs `notifications ack`,
// while a click on it still opens it, acknowledging nothing.
func TestAcknowledgeOnANotification(t *testing.T) {
	f := newFakeCLI(t)
	f.answer("notifications list --pending --json", `[{"id":"n7","at":"2026-11-23T09:00:00Z","kind":"run",`+
		`"title":"Door open","routine":"front-door","run":"r-7","urgent":true,"acknowledge":true}]`)
	f.answer("notifications open n7 --json", `{"notification":"n7","action":"show-run","routine":"front-door","run":"r-7"}`)
	tr, _, n, w := newTray(t, f)
	tr.notifier.deliver()
	want := Note{Summary: "Door open", Actions: []string{"default", "Open", "acknowledge", "Acknowledge"}, Persistent: true, Urgent: true}
	if got := n.notes[n.find("Door open")]; !reflect.DeepEqual(got, want) {
		t.Fatalf("posted %+v", got)
	}
	tr.Desk().Invoked(n.find("Door open"), "default")
	eventually(t, "the routine on its run", func() bool {
		return w.has(Window{Kind: RoutineWindow, Routine: "front-door", Run: "r-7"})
	})
	if f.hasRun("notifications ack n7 --json") {
		t.Fatal("a click acknowledged it")
	}
	tr.Desk().Invoked(n.find("Door open"), "acknowledge")
	eventually(t, "it to be acknowledged", func() bool { return f.hasRun("notifications ack n7 --json") })
}

// A click on one about a link opens the link (open-url), and nothing else.
func TestAClickOpensItsLink(t *testing.T) {
	f := newFakeCLI(t)
	f.answer("notifications list --pending --json", `[{"id":"n9","at":"2026-11-23T09:00:00Z","kind":"run",`+
		`"title":"Review requested","url":"https://github.com/acme/app/pull/7"}]`)
	f.answer("notifications open n9 --json", `{"notification":"n9","action":"open-url","url":"https://github.com/acme/app/pull/7"}`)
	tr, _, n, w := newTray(t, f)
	opened := make(chan string, 1)
	tr.openURL = func(u string) error { opened <- u; return nil }
	tr.notifier.deliver()
	tr.Desk().Invoked(n.find("Review requested"), "default")
	select {
	case u := <-opened:
		if u != "https://github.com/acme/app/pull/7" {
			t.Fatalf("opened %q", u)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the link didn't open")
	}
	if f.hasRun("notifications ack n9 --json") || w.has(Window{Kind: RoutinesWindow}) {
		t.Fatal("a click acknowledged it, or opened a window")
	}
}

// A click on a hook's run opens the hook's screen on it; one on a run that
// names no routine or hook, its report (`runs show --open`).
func TestAClickOnAHooksRunOrAnUnnamedOne(t *testing.T) {
	f := newFakeCLI(t)
	f.answer("notifications list --pending --json", pending)
	f.answer("notifications open n1 --json", `{"notification":"n1","action":"show-run","routine":"open-meet","run":"r-9","hook":true}`)
	f.answer("notifications open n2 --json", `{"notification":"n2","action":"show-run","run":"r-10"}`)
	tr, _, n, w := newTray(t, f)
	tr.notifier.deliver()
	tr.Desk().Invoked(n.find("standup failed"), "default")
	eventually(t, "the hook on its run", func() bool {
		return w.has(Window{Kind: HookWindow, Routine: "open-meet", Run: "r-9"})
	})
	tr.Desk().Invoked(n.find("Updated to v2.2.0"), "default")
	eventually(t, "the run's report", func() bool { return f.hasRun("runs show r-10 --open") })
}
