package notify

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
