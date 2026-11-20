package notify

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAddDeliverPrune(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notifications.json")
	now := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	old, _ := Add(path, Notification{Kind: KindFailure, Title: "old"}, now.Add(-40*24*time.Hour))
	a, err := Add(path, Notification{Kind: KindRoutine, Title: "a"}, now)
	if err != nil || a.ID == "" {
		t.Fatal(a, err)
	}
	f, _ := Load(path)
	if len(f.Notifications) != 1 || f.Notifications[0].ID != a.ID {
		t.Fatalf("old ones pruned past 30 days: %+v", f.Notifications)
	}
	if _, err := Get(path, old.ID); err == nil {
		t.Fatal("pruned notification still found")
	}
	if err := MarkDelivered(path, []string{a.ID}, now); err != nil {
		t.Fatal(err)
	}
	if n, _ := Get(path, a.ID); n.DeliveredAt == nil {
		t.Fatal("not marked delivered")
	}
	for i := range keep + 5 {
		_, _ = Add(path, Notification{Kind: KindRoutine, Title: "n"}, now.Add(time.Duration(i)*time.Second))
	}
	if f, _ := Load(path); len(f.Notifications) != keep {
		t.Fatalf("kept %d, want %d", len(f.Notifications), keep)
	}
}

func TestParseAction(t *testing.T) {
	for in, want := range map[string]Action{
		"show-run":            {Kind: ActionShowRun},
		" open  ~/report.md ": {Kind: ActionOpen, Target: "~/report.md"},
		"run say done":        {Kind: ActionRun, Target: "say done"},
		"none":                {Kind: ActionNone},
	} {
		if got, err := ParseAction(in); err != nil || got != want {
			t.Errorf("%q: %+v %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "open", "run ", "launch x", "none please"} {
		if _, err := ParseAction(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
