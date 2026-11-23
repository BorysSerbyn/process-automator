package notifications

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAddDeliverPrune(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notifications.json")
	now := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	old, _ := Add(path, Notification{Kind: KindFailure, Title: "old"}, now.Add(-40*24*time.Hour))
	a, err := Add(path, Notification{Kind: KindApp, Title: "a"}, now)
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
		_, _ = Add(path, Notification{Kind: KindApp, Title: "n"}, now.Add(time.Duration(i)*time.Second))
	}
	if f, _ := Load(path); len(f.Notifications) != keep {
		t.Fatalf("kept %d, want %d", len(f.Notifications), keep)
	}
}

// Routines' notifications that older versions kept here are dropped.
func TestRetiredKindsDropped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notifications.json")
	old := `{"schemaVersion":1,"notifications":[{"id":"1","at":"2026-11-19T09:00:00Z","kind":"routine","title":"r"},` +
		`{"id":"2","at":"2026-11-19T09:00:00Z","kind":"failure","title":"f"}]}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if f, err := Load(path); err != nil || len(f.Notifications) != 1 || f.Notifications[0].ID != "2" {
		t.Fatalf("%+v %v", f, err)
	}
}

// One waiting to be acknowledged waits until acknowledged, and is never
// pruned meanwhile; acknowledging an unknown one changes nothing.
func TestAcknowledge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notifications.json")
	now := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	w, _ := Add(path, Notification{Kind: KindRun, Title: "Ana asked for the report", Body: "b", Acknowledge: true}, now.Add(-40*24*time.Hour))
	plain, _ := Add(path, Notification{Kind: KindFailure, Title: "r failed", Acknowledge: true}, now)
	if plain.Waiting() || !w.Waiting() {
		t.Fatalf("waiting: %+v %+v", w, plain)
	}
	for i := range keep + 5 {
		_, _ = Add(path, Notification{Kind: KindApp, Title: "n"}, now.Add(time.Duration(i)*time.Second))
	}
	f, _ := Load(path)
	if len(f.Notifications) != keep+1 || len(f.Waiting()) != 1 || f.Waiting()[0].ID != w.ID {
		t.Fatalf("kept %d, waiting %+v", len(f.Notifications), f.Waiting())
	}
	if _, err := Acknowledge(path, []string{w.ID, "nope"}, false, now); err == nil {
		t.Fatal("an unknown one acknowledged")
	}
	if f, _ := Load(path); len(f.Waiting()) != 1 {
		t.Fatal("a failed acknowledgement acknowledged")
	}
	done, err := Acknowledge(path, []string{w.ID}, false, now)
	if err != nil || len(done) != 1 || done[0].AcknowledgedAt == nil {
		t.Fatal(done, err)
	}
	if done, err := Acknowledge(path, []string{w.ID}, false, now); err != nil || len(done) != 0 {
		t.Fatal("acknowledged twice", done, err)
	}
	b, _ := Add(path, Notification{Kind: KindRun, Title: "b", Body: "b", Acknowledge: true}, now)
	c, _ := Add(path, Notification{Kind: KindRun, Title: "c", Body: "c", Acknowledge: true}, now)
	if done, _ := Acknowledge(path, nil, true, now); len(done) != 2 || done[0].ID != b.ID || done[1].ID != c.ID {
		t.Fatalf("all: %+v", done)
	}
	if f, _ := Load(path); len(f.Waiting()) != 0 {
		t.Fatal("still waiting")
	}
}
