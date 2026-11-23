package tray

import (
	"reflect"
	"testing"
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

// A click asks the core what to show: a run opens it (`runs show --open`),
// the app opens its routines (the window seam). Other actions do nothing.
func TestAClickShowsWhatTheCoreSays(t *testing.T) {
	f := newFakeCLI(t)
	f.answer("notifications list --pending --json", pending)
	f.answer("notifications open n1 --json", `{"notification":"n1","action":"show-run","routine":"standup","run":"r-42"}`)
	f.answer("notifications open n2 --json", `{"notification":"n2","action":"show-app"}`)
	tr, _, n, w := newTray(t, f)
	tr.notifier.deliver()

	tr.Desk().Invoked(n.find("standup failed"), "default")
	eventually(t, "the run to open", func() bool { return f.hasRun("runs show r-42 --open") })
	if !f.hasRun("notifications open n1 --json") {
		t.Fatalf("%q", f.ran())
	}
	tr.Desk().Invoked(n.find("Updated to v2.2.0"), "default")
	eventually(t, "the routines window", func() bool { return w.has(Window{Kind: RoutinesWindow}) })

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
	f.answer("notifications list --pending --json", `[{"id":"n7","at":"2026-11-23T09:00:00Z","kind":"acknowledge",`+
		`"title":"Door open","routine":"front-door","run":"r-7","urgent":true,"acknowledge":true}]`)
	f.answer("notifications open n7 --json", `{"notification":"n7","action":"show-run","routine":"front-door","run":"r-7"}`)
	tr, _, n, _ := newTray(t, f)
	tr.notifier.deliver()
	want := Note{Summary: "Door open", Actions: []string{"default", "Open", "acknowledge", "Acknowledge"}, Persistent: true, Urgent: true}
	if got := n.notes[n.find("Door open")]; !reflect.DeepEqual(got, want) {
		t.Fatalf("posted %+v", got)
	}
	tr.Desk().Invoked(n.find("Door open"), "default")
	eventually(t, "the run to open", func() bool { return f.hasRun("runs show r-7 --open") })
	if f.hasRun("notifications ack n7 --json") {
		t.Fatal("a click acknowledged it")
	}
	tr.Desk().Invoked(n.find("Door open"), "acknowledge")
	eventually(t, "it to be acknowledged", func() bool { return f.hasRun("notifications ack n7 --json") })
}
