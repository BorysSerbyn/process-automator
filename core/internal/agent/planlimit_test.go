package agent

import (
	"testing"
	"time"
)

func TestPlanLimitError(t *testing.T) {
	now := time.Date(2026, 11, 20, 14, 0, 0, 0, time.Local) // a Friday
	for msg, want := range map[string]time.Time{
		"You've hit your session limit · resets 3:45pm":     time.Date(2026, 11, 20, 15, 45, 0, 0, time.Local),
		"You've hit your session limit · resets 9am":        time.Date(2026, 11, 21, 9, 0, 0, 0, time.Local),
		"You've hit your weekly limit · resets Mon 12:00am": time.Date(2026, 11, 23, 0, 0, 0, 0, time.Local),
		"You've hit your weekly limit · resets Nov 25 4pm":  time.Date(2026, 11, 25, 16, 0, 0, 0, time.Local),
		"You've hit your session limit":                     {},
	} {
		e := planLimitError(msg, now)
		if e == nil || !e.ResetsAt.Equal(want) {
			t.Errorf("%q: %+v, want reset %v", msg, e, want)
		}
	}
	if e := planLimitError("You've hit your weekly limit · resets Mon 12:00am", now); e.Window != "weekly" {
		t.Errorf("window %q", e.Window)
	}
	if planLimitError("rate limited by the server", now) != nil {
		t.Error("an unrelated error read as a plan limit")
	}
}
