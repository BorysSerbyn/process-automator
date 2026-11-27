package engine

import (
	"testing"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/notifications"
)

// Setup with steps left is told once when the daemon starts, and again only
// when a step is added (or comes back), not on every pass.
func TestSetupNotification(t *testing.T) {
	a := testApp(t)
	n := &setupNotifier{}
	now := time.Now()
	told := func() []notifications.Notification {
		t.Helper()
		f, err := notifications.Load(a.Layout.Notifications())
		if err != nil {
			t.Fatal(err)
		}
		return f.Notifications
	}
	passes := []struct {
		left []string
		want int // notifications so far
	}{
		{nil, 0},                                    // set up: nothing to tell
		{[]string{"Model backend"}, 1},              // started with a step left
		{[]string{"Model backend"}, 1},              // the same step: told already
		{[]string{"Model backend", "Turn on r"}, 2}, // a new routine to turn on
		{[]string{"Turn on r"}, 2},                  // one done
		{nil, 2},                                    // all done
		{[]string{"Turn on r"}, 3},                  // back again
	}
	for i, p := range passes {
		n.record(a, p.left, now)
		if got := told(); len(got) != p.want {
			t.Fatalf("pass %d (%v): %d notifications, want %d", i, p.left, len(got), p.want)
		}
	}
	last := told()[2]
	if last.Kind != notifications.KindApp || !last.Setup || last.Body != "Still to do: Turn on r" {
		t.Fatalf("notification: %+v", last)
	}
}
