package cli

import (
	"strconv"
	"time"
)

// The routine windows' calls: the commands automator-ui's Routines window,
// a routine's window and the Updating window run, with the same arguments.

// RoutineInfo is a routine with its effective settings (`routines list
// --json`, and `routines show --json`).
type RoutineInfo struct {
	Name string `json:"name"`
	// Kind is program for a program; absent for an LLM routine.
	Kind         string   `json:"kind,omitempty"`
	Path         string   `json:"path"`
	Servers      []string `json:"servers"`
	Tools        []string `json:"tools"`
	Model        string   `json:"model,omitempty"`
	Enabled      bool     `json:"enabled"`
	ScheduleText string   `json:"scheduleText,omitempty"`
	BuiltinTools []string `json:"builtinTools"`
	Folder       string   `json:"folder,omitempty"`
	Trusted      bool     `json:"trusted"`
	// Untrusted says why it isn't trusted, in the core's words.
	Untrusted string `json:"untrusted,omitempty"`
	// TurnOn is true while turning it on has something to do: Turn On…
	TurnOn     bool       `json:"turnOn,omitempty"`
	AgentModel string     `json:"agentModel,omitempty"`
	Next       *time.Time `json:"next,omitempty"`
	LastRun    *LastRun   `json:"lastRun,omitempty"`
	// Program is a program's own: what runs.
	Program *ProgramInfo `json:"program,omitempty"`
	// Description is what its file says it does.
	Description string `json:"description,omitempty"`
}

// RoutineDetail is a routine in full (`routines show --json`): its row, and
// its file as written (a program's: its description file).
type RoutineDetail struct {
	RoutineInfo
	Source string `json:"source"`
}

// IsProgram is whether it's a program (an executable), not an LLM routine:
// it has no servers, tools, model or memories.
func (r RoutineInfo) IsProgram() bool { return r.Kind == "program" }

// ProgramInfo is what a program runs: the app's copy of its executable, and
// what its description file declares.
type ProgramInfo struct {
	Executable string          `json:"executable"`
	Checksum   string          `json:"checksum,omitempty"`
	Args       []string        `json:"args"`
	Writes     bool            `json:"writes"`
	Secrets    []ProgramSecret `json:"secrets"`
}

// ProgramSecret is a secret a program declares, and whether it's stored.
type ProgramSecret struct {
	Name   string `json:"name"`
	Env    string `json:"env"`
	Stored bool   `json:"stored"`
}

// LastRun is a routine's latest run, as run status and `routines list` give it.
type LastRun struct {
	// ID is the run's history ID (as `runs show` takes it).
	ID      string `json:"id,omitempty"`
	Trigger string `json:"trigger,omitempty"`
	// StartedAt is missing from a run waiting for its turn.
	StartedAt *time.Time `json:"startedAt,omitempty"`
	// Outcome is success, failed, skipped, missed, needs-reauth, running,
	// queued, stopped or interrupted.
	Outcome string `json:"outcome"`
	Reason  string `json:"reason,omitempty"`
}

// InProgress is whether the run is going, or waiting for its turn.
func (r *LastRun) InProgress() bool {
	return r != nil && (r.Outcome == "running" || r.Outcome == "queued")
}

// RunSummary is one run in a routine's history (`runs <routine> --json`).
type RunSummary struct {
	ID        string    `json:"id"`
	Trigger   string    `json:"trigger"`
	StartedAt time.Time `json:"startedAt"`
	Outcome   string    `json:"outcome"`
	Reason    string    `json:"reason,omitempty"`
	ToolCalls *int      `json:"toolCalls,omitempty"`
	CostUSD   *float64  `json:"costUsd,omitempty"`
}

// RunEntry is one run in full (`runs show <id> --json`).
type RunEntry struct {
	ID string `json:"id"`
	// Hook is true for a hook's run; Events are what it ran for.
	Hook       bool       `json:"hook,omitempty"`
	Events     []Event    `json:"events,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Outcome    string     `json:"outcome"`
	Reason     string     `json:"reason,omitempty"`
	Error      string     `json:"error,omitempty"`
	Result     *RunResult `json:"result,omitempty"`
	// Program is a program's run (an LLM routine's is Result).
	Program *ProgramRun `json:"program,omitempty"`
}

// ProgramRun is what a program did in a run.
type ProgramRun struct {
	ExitCode int    `json:"exitCode"`
	Summary  string `json:"summary,omitempty"`
	// Output is what it printed: all of it, or its first and last part with
	// what was left out marked between them (OutputTruncated).
	Output          string `json:"output,omitempty"`
	OutputTruncated bool   `json:"outputTruncated,omitempty"`
	DryRun          bool   `json:"dryRun"`
}

// RunResult is what the model did in a run.
type RunResult struct {
	Text   string  `json:"text"`
	Writes []Write `json:"writes"`
	// Calls are every tool call, in order (absent from older records).
	Calls        []ToolCall `json:"calls,omitempty"`
	DryRun       bool       `json:"dryRun"`
	Backend      string     `json:"backend"`
	InputTokens  int        `json:"inputTokens"`
	OutputTokens int        `json:"outputTokens"`
	CachedTokens int        `json:"cachedTokens"`
	// Outcome is the routine's own report: done or nothing_to_do.
	Outcome string `json:"outcome,omitempty"`
}

// Write is a tool call that can change things (older records list only these).
type Write struct {
	Tool  string         `json:"tool"`
	Args  map[string]any `json:"args"`
	Error string         `json:"error,omitempty"`
}

// ToolCall is one tool call a run made (a write: one that can change things).
type ToolCall struct {
	Tool  string         `json:"tool"`
	Args  map[string]any `json:"args"`
	Write bool           `json:"write,omitempty"`
	Error string         `json:"error,omitempty"`
}

// Memory is one of a routine's memories (`memory list --routine`).
type Memory struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// Routines is every routine of either kind, LLM routines and programs
// together, with its effective settings (`routines list --kind all`).
func (c CLI) Routines() ([]RoutineInfo, error) {
	var out []RoutineInfo
	return out, c.call(&out, "routines", "list", "--kind", "all")
}

// Routine is one routine in full (`routines show`).
func (c CLI) Routine(name string) (RoutineDetail, error) {
	var out RoutineDetail
	return out, c.call(&out, "routines", "show", name)
}

// RoutineSettings are a routine's own settings (`settings list --routine`).
func (c CLI) RoutineSettings(name string) ([]Setting, error) {
	var out []Setting
	return out, c.call(&out, "settings", "list", "--routine", name)
}

// Runs is a routine's run history, newest first (`runs <routine> --limit`).
func (c CLI) Runs(routine string, limit int) ([]RunSummary, error) {
	var out []RunSummary
	return out, c.call(&out, "runs", routine, "--limit", strconv.Itoa(limit))
}

// Run is one run in full (`runs show`).
func (c CLI) Run(id string) (RunEntry, error) {
	var out RunEntry
	return out, c.call(&out, "runs", "show", id)
}

// StartRun starts a routine's run in the background (`start [--dry-run]`);
// run status follows it.
func (c CLI) StartRun(routine string, dryRun bool) error {
	args := []string{"start", routine}
	if dryRun {
		args = append(args, "--dry-run")
	}
	var out map[string]any
	return c.call(&out, args...)
}

// StopRun stops a routine's run in progress, or waiting for its turn (`stop`).
func (c CLI) StopRun(routine string) error {
	var out map[string]any
	return c.call(&out, "stop", routine)
}

// SetPaused pauses a routine's schedule (`routines pause`), or resumes it.
func (c CLI) SetPaused(routine string, paused bool) error {
	verb := "resume"
	if paused {
		verb = "pause"
	}
	var out map[string]any
	return c.call(&out, "routines", verb, routine)
}

// DeleteRoutine deletes a routine: its file, settings, trust and memories
// (`routines delete`).
func (c CLI) DeleteRoutine(routine string) error {
	var out map[string]any
	return c.call(&out, "routines", "delete", routine)
}

// Trust trusts a routine with what it can reach, after its review
// (`routines trust <routine> --yes`).
func (c CLI) Trust(routine string) (TrustOutput, error) {
	var out TrustOutput
	return out, c.call(&out, "routines", "trust", routine, "--yes")
}

// Memories are a routine's own memories (`memory list --routine`).
func (c CLI) Memories(routine string) ([]Memory, error) {
	var out []Memory
	return out, c.call(&out, "memory", "list", "--routine", routine)
}

// AddMemory adds a memory to a routine's own; the text goes after `--`, so
// one starting with "-" isn't read as a flag.
func (c CLI) AddMemory(routine, text string) error {
	_, err := c.Exec("memory", "add", "--routine", routine, "--json", "--", text)
	return err
}

// RemoveMemory forgets a memory (`memory rm`).
func (c CLI) RemoveMemory(id string) error {
	_, err := c.Exec("memory", "rm", id)
	return err
}
