// Package history keeps one record per routine run on disk:
// <dir>/<routine>/<started>.json. Every run that was due leaves a record:
// executed runs (any outcome), missed slots and runs cut off by a restart. Dry
// runs leave none. `procauto runs` reads it; retention prunes it.
package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/atomicfile"
	"github.com/BorysSerbyn/process-automator/core/internal/programs"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
)

// stamp names a record after its start time (seconds; unique per routine).
const stamp = "2006-01-02T150405"

// RunID is the ID a run started at started gets in history, e.g.
// "standup/2026-11-19T092022", known before the run is saved.
func RunID(routine string, started time.Time) string {
	return routine + "/" + started.Format(stamp)
}

// Entry is one run's record.
type Entry struct {
	// ID is "<routine>/<started>", e.g. "standup/2026-11-19T092022".
	ID      string `json:"id"`
	Routine string `json:"routine"`
	// Kind is the routine's kind: program for a program; absent for an LLM
	// routine (llm).
	Kind string `json:"kind,omitempty" jsonschema:"enum=llm,enum=program"`
	// Trigger is what started the run: "schedule", "manual" or "backfill".
	Trigger string `json:"trigger"`
	// TriggerID identifies the occurrence, e.g. "schedule:2026-11-19T09:20:00".
	TriggerID string `json:"triggerId,omitempty"`
	// For is the time the run is for: the schedule slot, the backfilled day's
	// time, or the start of a manual run. Its inputs (today, since) follow it.
	For *time.Time `json:"for,omitempty"`
	// SessionID is the run's saved agent CLI session, if it kept one.
	SessionID string `json:"sessionId,omitempty"`
	// Folder is the folder the run worked in (where its session resumes
	// from), unless it was an empty temporary one.
	Folder string `json:"folder,omitempty"`
	// Slot is how records written before occurrences said For (schedule only).
	Slot       *time.Time `json:"slot,omitempty"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	// Outcome and Reason are the run's result, as recorded in run status.
	Outcome runstatus.Outcome `json:"outcome"`
	Reason  string            `json:"reason,omitempty"`
	// Inputs the routine ran with (e.g. today, since): what the run covered.
	Inputs map[string]any `json:"inputs,omitempty"`
	// Result is the agent's result, for an LLM routine's runs that got as
	// far as the model.
	Result *agent.Result `json:"result,omitempty"`
	// Program is how a program's run went, for runs that got as far as
	// starting it.
	Program *programs.Execution `json:"program,omitempty"`
	// Error is how records written before outcomes were recorded said a run failed.
	Error string `json:"error,omitempty"`
}

// Summary is an entry without the agent result, for listings.
type Summary struct {
	ID      string `json:"id"`
	Routine string `json:"routine"`
	// Kind is the routine's kind: program for a program; absent for an LLM
	// routine (llm).
	Kind       string            `json:"kind,omitempty" jsonschema:"enum=llm,enum=program"`
	Trigger    string            `json:"trigger"`
	For        *time.Time        `json:"for,omitempty"`
	SessionID  string            `json:"sessionId,omitempty"`
	Folder     string            `json:"folder,omitempty"`
	StartedAt  time.Time         `json:"startedAt"`
	FinishedAt *time.Time        `json:"finishedAt,omitempty"`
	Outcome    runstatus.Outcome `json:"outcome"`
	Reason     string            `json:"reason,omitempty"`
	ToolCalls  int               `json:"toolCalls,omitempty"`
	// Backend is what ran it (an llm.backend value), when it got as far as the model.
	Backend string `json:"backend,omitempty"`
	// CostUSD is the run's cost at API list prices: claude-api runs only (a Claude
	// plan's runs have no cost).
	CostUSD float64 `json:"costUsd,omitempty"`
}

// Save writes e (its ID is set from routine and start time).
func Save(dir string, e Entry) error {
	e.ID = RunID(e.Routine, e.StartedAt)
	path := filepath.Join(dir, e.Routine, e.StartedAt.Format(stamp)+".json")
	return atomicfile.WriteJSON(path, e)
}

// Filter selects runs for List. Zero values match everything.
type Filter struct {
	Routine string
	Since   time.Time
	Limit   int // newest first; 0 = no limit
}

// List returns run summaries, newest first. A missing history is empty.
func List(dir string, f Filter) ([]Summary, error) {
	pattern := filepath.Join(dir, "*", "*.json")
	if f.Routine != "" {
		pattern = filepath.Join(dir, f.Routine, "*.json")
	}
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	var out []Summary
	for _, p := range paths {
		e, err := read(p)
		if err != nil {
			return nil, err
		}
		if !f.Since.IsZero() && e.StartedAt.Before(f.Since) {
			continue
		}
		s := Summary{
			ID: e.ID, Routine: e.Routine, Kind: e.Kind, Trigger: e.Trigger, For: e.For, SessionID: e.SessionID, Folder: e.Folder, StartedAt: e.StartedAt,
			FinishedAt: e.FinishedAt, Outcome: e.Outcome, Reason: e.Reason,
		}
		if e.Result != nil {
			s.ToolCalls, s.CostUSD, s.Backend = e.Result.ToolCalls, e.Result.CostUSD, e.Result.Backend
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

// ErrNotFound means no run has that ID.
var ErrNotFound = errors.New("no such run")

// Get returns one run by ID ("<routine>/<started>").
func Get(dir, id string) (*Entry, error) {
	routine, started, ok := strings.Cut(id, "/")
	if !ok || routine == "" || started == "" || strings.ContainsAny(started, `/\`) || strings.Contains(routine, "..") {
		return nil, fmt.Errorf("run id %q: want <routine>/<started>, e.g. standup/2026-11-19T092022", id)
	}
	e, err := read(filepath.Join(dir, routine, started+".json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return e, err
}

// Prune deletes records older than cutoff.
func Prune(dir string, cutoff time.Time) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil || d.IsDir() {
			return err
		}
		if info, err := d.Info(); err == nil && info.ModTime().Before(cutoff) {
			return os.Remove(path)
		}
		return nil
	})
}

// read loads a record; older records get their ID and outcome filled in.
func read(path string) (*Entry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var e Entry
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if e.ID == "" {
		e.ID = e.Routine + "/" + strings.TrimSuffix(filepath.Base(path), ".json")
	}
	if e.Result != nil && e.Result.Writes == nil { // written as null before runs started with an empty list
		e.Result.Writes = []agent.Write{}
	}
	if e.For == nil && e.Slot != nil { // written before occurrences
		e.For, e.Slot = e.Slot, nil
	}
	if e.Outcome == "" { // written before outcomes were recorded
		switch {
		case strings.Contains(e.Error, "needs re-authentication"):
			e.Outcome, e.Reason = runstatus.OutcomeNeedsReauth, e.Error
		case e.Error != "":
			e.Outcome, e.Reason = runstatus.OutcomeFailed, e.Error
		default:
			e.Outcome = runstatus.OutcomeSuccess
		}
	}
	return &e, nil
}
