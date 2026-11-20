package usage

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAssessBudget(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	b := Budgets{Window: 5 * time.Hour, WindowUSD: 1, WeeklyUSD: 20}
	runs := []Run{
		{now.Add(-time.Hour), 0.6},
		{now.Add(-4 * time.Hour), 0.5}, // in both windows: $1.10 > $1
		{now.Add(-6 * time.Hour), 3},   // week only
		{now.Add(-8 * 24 * time.Hour), 50},
	}
	r := AssessBudget(runs, b, now)
	if r.Kind != "budget" || !r.Over || !strings.Contains(r.Reason, "last 5h is $1.10, past its $1.00 budget") {
		t.Errorf("over the short window: %+v", r)
	}
	if w := r.Windows[1]; w.Length != "7d" || *w.SpentUSD < 4.09 || *w.SpentUSD > 4.11 || w.Over {
		t.Errorf("week: %+v", w)
	}
	if r := AssessBudget(runs, Budgets{Window: 5 * time.Hour}, now); r.Over {
		t.Errorf("no budgets: %+v", r)
	}
}

func TestPlan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan-usage.json")
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.Local)
	reads := 0
	limits := []Limit{
		{Kind: "session", Percent: 62, ResetsAt: now.Add(2 * time.Hour)},
		{Kind: "weekly_all", Percent: 20, ResetsAt: now.Add(72 * time.Hour)},
		{Kind: "weekly_scoped", Model: "Fable", Percent: 95, ResetsAt: now.Add(72 * time.Hour)},
	}
	fetch := func() ([]Limit, error) { reads++; return limits, nil }
	p, err := Refresh(path, 10*time.Minute, now, fetch)
	if err != nil {
		t.Fatal(err)
	}
	r := AssessPlan(p, 50, now)
	if !r.Over || !strings.Contains(r.Reason, "5-hour usage is at 62%, past llm.usage-limit (50%)") || r.FetchedAt == nil {
		t.Errorf("over 50%%: %+v", r)
	}
	if len(r.Models) != 1 || r.Models[0].Model != "Fable" {
		t.Errorf("models: %+v", r.Models)
	}
	if r := AssessPlan(p, 80, now); r.Over {
		t.Errorf("a single model's weekly limit applied: %+v", r)
	}
	// A fresh reading isn't read again; an old one is.
	_, _ = Refresh(path, 10*time.Minute, now.Add(5*time.Minute), fetch)
	if reads != 1 {
		t.Errorf("read %d times within 10 minutes", reads)
	}
	_, _ = Refresh(path, 10*time.Minute, now.Add(11*time.Minute), fetch)
	if reads != 2 {
		t.Errorf("an old reading wasn't read again (%d reads)", reads)
	}
	// Past its reset, a window is back at 0%.
	if r := AssessPlan(p, 50, now.Add(3*time.Hour)); r.Over || *r.Windows[0].UsedPercent != 0 {
		t.Errorf("after the reset: %+v", r)
	}
	// A failing read keeps the last reading and says why.
	failing := func() ([]Limit, error) { return nil, errors.New("rate limited") }
	p, _ = Refresh(path, 10*time.Minute, now.Add(30*time.Minute), failing)
	if r := AssessPlan(p, 50, now.Add(30*time.Minute)); !r.Over || r.ReadError != "rate limited" {
		t.Errorf("after a failed read: %+v", r)
	}
	// Nothing known: never over.
	empty, _ := Refresh(filepath.Join(t.TempDir(), "p.json"), time.Minute, now, failing)
	if r := AssessPlan(empty, 50, now); r.Over {
		t.Errorf("unknown plan: %+v", r)
	}
	// A run hit the weekly limit: over until it resets, whatever the limit.
	if err := RecordLimitHit(path, "weekly", now.Add(24*time.Hour), now); err != nil {
		t.Fatal(err)
	}
	p, _ = LoadPlan(path)
	if r := AssessPlan(p, 100, now); !r.Over || !strings.Contains(r.Reason, "weekly limit is reached") {
		t.Errorf("limit hit: %+v", r)
	}
	// Without a reset time, it's tried again in an hour.
	_ = RecordLimitHit(path, "session", time.Time{}, now)
	p, _ = LoadPlan(path)
	if !p.Session.ResetsAt.Equal(now.Add(time.Hour)) {
		t.Errorf("unknown reset: %v", p.Session.ResetsAt)
	}
}
