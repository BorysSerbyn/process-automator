package history

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
)

var t0 = time.Date(2026, 11, 19, 9, 20, 22, 0, time.UTC)

func TestSaveListGet(t *testing.T) {
	dir := t.TempDir()
	fin := t0.Add(time.Minute)
	runs := []Entry{
		{Routine: "scrum", Trigger: "schedule", StartedAt: t0, FinishedAt: &fin, Outcome: runstatus.OutcomeFailed,
			Reason: "send_message failed", Inputs: map[string]any{"today": "Thursday 2026-11-19"},
			Result: &agent.Result{ToolCalls: 6, CostUSD: 0.12, Text: "couldn't post"}},
		{Routine: "timesheets", Trigger: "manual", StartedAt: t0.Add(time.Hour), Outcome: runstatus.OutcomeSuccess},
		{Routine: "scrum", Trigger: "schedule", StartedAt: t0.Add(-24 * time.Hour), Outcome: runstatus.OutcomeMissed},
	}
	for _, e := range runs {
		if err := Save(dir, e); err != nil {
			t.Fatal(err)
		}
	}

	all, err := List(dir, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].Routine != "timesheets" || all[2].Outcome != runstatus.OutcomeMissed {
		t.Fatalf("newest first: %+v", all)
	}
	if all[1].ID != "scrum/2026-11-19T092022" || all[1].ToolCalls != 6 || all[1].CostUSD != 0.12 {
		t.Fatalf("summary: %+v", all[1])
	}
	if got, _ := List(dir, Filter{Routine: "scrum", Since: t0.Add(-time.Hour)}); len(got) != 1 || got[0].Routine != "scrum" {
		t.Fatalf("routine + since filter: %+v", got)
	}
	if got, _ := List(dir, Filter{Limit: 1}); len(got) != 1 {
		t.Fatalf("limit: %+v", got)
	}

	e, err := Get(dir, "scrum/2026-11-19T092022")
	if err != nil || e.Result.Text != "couldn't post" || e.Inputs["today"] != "Thursday 2026-11-19" {
		t.Fatalf("get: %+v %v", e, err)
	}
	if _, err := Get(dir, "scrum/2020-01-01T000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	for _, bad := range []string{"scrum", "../x/y", "scrum/../../etc"} {
		if _, err := Get(dir, bad); err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("%q should be rejected as a malformed id, got %v", bad, err)
		}
	}
}

func TestEmptyAndOldRecords(t *testing.T) {
	if got, err := List(filepath.Join(t.TempDir(), "none"), Filter{}); err != nil || len(got) != 0 {
		t.Fatalf("missing history: %v %v", got, err)
	}
	// Written before outcomes were recorded: outcome comes from the error.
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "scrum"), 0o700)
	old := map[string]string{
		"2026-11-18T092000.json": `{"routine":"scrum","trigger":"schedule","startedAt":"2026-11-18T09:20:00Z","finishedAt":"2026-11-18T09:21:00Z"}`,
		"2026-11-17T092000.json": `{"routine":"scrum","trigger":"schedule","startedAt":"2026-11-17T09:20:00Z","error":"boom"}`,
	}
	for name, body := range old {
		_ = os.WriteFile(filepath.Join(dir, "scrum", name), []byte(body), 0o600)
	}
	got, err := List(dir, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Outcome != runstatus.OutcomeSuccess || got[0].ID != "scrum/2026-11-18T092000" || got[1].Outcome != runstatus.OutcomeFailed || got[1].Reason != "boom" {
		t.Fatalf("old records: %+v", got)
	}
}

func TestPrune(t *testing.T) {
	dir := t.TempDir()
	_ = Save(dir, Entry{Routine: "scrum", StartedAt: t0, Outcome: runstatus.OutcomeSuccess})
	_ = Save(dir, Entry{Routine: "scrum", StartedAt: t0.Add(time.Hour), Outcome: runstatus.OutcomeSuccess})
	old := filepath.Join(dir, "scrum", t0.Format(stamp)+".json")
	_ = os.Chtimes(old, t0, t0)
	if err := Prune(dir, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got, _ := List(dir, Filter{}); len(got) != 1 {
		t.Fatalf("after prune: %+v", got)
	}
	if err := Prune(filepath.Join(dir, "none"), t0); err != nil {
		t.Fatalf("pruning a missing history: %v", err)
	}
}
