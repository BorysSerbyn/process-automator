package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// The hook and watcher windows' calls: the commands automator-ui's Hooks
// window, a hook's window and a watcher's run, with the same arguments.

// On is what a hook acts on: a watcher's events, of one type (empty: every type).
type On struct {
	Watcher string `json:"watcher"`
	Type    string `json:"type,omitempty"`
}

// HookInfo is a hook with its effective settings (`hooks list --json`).
type HookInfo struct {
	Name string `json:"name"`
	// Kind is program for a program hook; absent for an LLM hook.
	Kind        string `json:"kind,omitempty"`
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
	On          On     `json:"on"`
	// Batch (an LLM hook's) gives one run every event that waited for it.
	Batch     bool   `json:"batch,omitempty"`
	Enabled   bool   `json:"enabled"`
	Staleness string `json:"staleness"`
	Timeout   string `json:"timeout"`
	Rate      int    `json:"rate"`
	Folder    string `json:"folder,omitempty"`
	Trusted   bool   `json:"trusted"`
	Untrusted string `json:"untrusted,omitempty"`
	// TurnOn is true while turning it on has something to do (it's off, or
	// it or its watcher isn't trusted): Turn On…
	TurnOn bool `json:"turnOn,omitempty"`
	// Problem is why it can't run while it's on, in the core's words.
	Problem      string       `json:"problem,omitempty"`
	Servers      []string     `json:"servers,omitempty"`
	Tools        []string     `json:"tools,omitempty"`
	BuiltinTools []string     `json:"builtinTools,omitempty"`
	Model        string       `json:"model,omitempty"`
	Program      *ProgramInfo `json:"program,omitempty"`
	LastRun      *LastRun     `json:"lastRun,omitempty"`
	// Permissions are what the OS must allow it, with their state.
	Permissions []PermissionNeed `json:"permissions,omitempty"`
}

// IsProgram is whether it's a program hook, not an LLM hook: it has no
// servers, tools, model or memories.
func (h HookInfo) IsProgram() bool { return h.Kind == "program" }

// HookDetail is a hook in full (`hooks show --json`): its row, its file as
// written, and its watcher (nil: there's none by that name).
type HookDetail struct {
	HookInfo
	Source  string       `json:"source"`
	Watcher *WatcherInfo `json:"watcher,omitempty"`
}

// WatcherInfo is a watcher (`watchers list --json`).
type WatcherInfo struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
	// Mode is poll or stream; Interval and Timeout are a poll watcher's.
	Mode       string          `json:"mode"`
	Interval   string          `json:"interval,omitempty"`
	Timeout    string          `json:"timeout,omitempty"`
	Executable string          `json:"executable"`
	Checksum   string          `json:"checksum,omitempty"`
	Args       []string        `json:"args"`
	Secrets    []ProgramSecret `json:"secrets"`
	// Enabled is false while it's paused.
	Enabled   bool   `json:"enabled"`
	Trusted   bool   `json:"trusted"`
	Untrusted string `json:"untrusted,omitempty"`
	// Hooks are the hooks listening to it.
	Hooks       []string   `json:"hooks"`
	LastPollAt  *time.Time `json:"lastPollAt,omitempty"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	LastEventAt *time.Time `json:"lastEventAt,omitempty"`
	// Error is why its last poll failed or its stream ended early, and
	// ErrorAt when.
	Error   string     `json:"error,omitempty"`
	ErrorAt *time.Time `json:"errorAt,omitempty"`
	// Permissions are what the OS must allow it, with their state.
	Permissions []PermissionNeed `json:"permissions,omitempty"`
}

// WatcherDetail is a watcher in full (`watchers show --json`).
type WatcherDetail struct {
	WatcherInfo
	Source string `json:"source"`
}

// Program is what trusting a watcher approves, as a program's rows show it.
func (w WatcherInfo) Program() ProgramInfo {
	return ProgramInfo{Executable: w.Executable, Checksum: w.Checksum, Args: w.Args, Secrets: w.Secrets}
}

// WatcherTrustOutput is `watchers trust --json`: what trusting it approves.
type WatcherTrustOutput struct {
	Watcher string      `json:"watcher"`
	Mode    string      `json:"mode"`
	Program ProgramInfo `json:"program"`
	Changes []string    `json:"changes"`
	Trusted bool        `json:"trusted"`
	// Permissions are what the OS must allow it, which trusting it covers.
	Permissions []PermissionNeed `json:"permissions,omitempty"`
}

// Event is one thing that happened, as a watcher printed it: a hook's run
// was for it. Data is outside data.
type Event struct {
	ID      string         `json:"id"`
	Type    string         `json:"type"`
	At      time.Time      `json:"at"`
	Watcher string         `json:"watcher,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
}

// DataText is an event's data as plain text: indented JSON, keys in order
// ("" when it has none).
func (e Event) DataText() string {
	if len(e.Data) == 0 {
		return ""
	}
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false) // as the watcher wrote it: < stays <
	enc.SetIndent("", "  ")
	if err := enc.Encode(e.Data); err != nil {
		return fmt.Sprint(e.Data)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// FireOutput is `hooks fire --json`: how the run went (an LLM hook's
// agent result, or a program hook's run).
type FireOutput struct {
	Kind    string      `json:"kind,omitempty"`
	Text    string      `json:"text,omitempty"`
	Reason  string      `json:"reason,omitempty"`
	Outcome string      `json:"outcome,omitempty"`
	DryRun  bool        `json:"dryRun,omitempty"`
	Program *ProgramRun `json:"program,omitempty"`
}

// Hooks is every hook (`hooks list`).
func (c CLI) Hooks() ([]HookInfo, error) {
	var out []HookInfo
	return out, c.call(&out, "hooks", "list")
}

// Hook is one hook in full, with its watcher (`hooks show`).
func (c CLI) Hook(name string) (HookDetail, error) {
	var out HookDetail
	return out, c.call(&out, "hooks", "show", name)
}

// HookTrustCheck is what a hook acts on and can reach, trusting nothing
// (`hooks trust --check`).
func (c CLI) HookTrustCheck(name string) (TrustOutput, error) {
	var out TrustOutput
	return out, c.call(&out, "hooks", "trust", name, "--check")
}

// TrustHook trusts a hook after its review (`hooks trust --yes`); turnOn
// also turns it on (`--resume`).
func (c CLI) TrustHook(name string, turnOn bool) error {
	args := []string{"hooks", "trust", name, "--yes"}
	if turnOn {
		args = append(args, "--resume")
	}
	var out TrustOutput
	return c.call(&out, args...)
}

// SetHookPaused turns a hook off (`hooks pause`), or on again (`hooks resume`).
func (c CLI) SetHookPaused(name string, paused bool) error {
	verb := "resume"
	if paused {
		verb = "pause"
	}
	var out map[string]any
	return c.call(&out, "hooks", verb, name)
}

// DeleteHook deletes a hook: its copy, settings, trust and secrets (`hooks delete`).
func (c CLI) DeleteHook(name string) error {
	var out map[string]any
	return c.call(&out, "hooks", "delete", name)
}

// FireHook runs a hook for a sample event (`hooks fire --event-json`), as
// a dry run or for real. It waits for the run, which its own timeout ends:
// no time limit here.
func (c CLI) FireHook(name, event string, dryRun bool) (FireOutput, error) {
	args := []string{"hooks", "fire", name, "--event-json", event}
	if dryRun {
		args = append(args, "--dry-run")
	}
	stdout, err := c.exec(0, append(args, "--json"))
	var out FireOutput
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(stdout, &out); err != nil {
		return out, &Failure{Message: fmt.Sprintf("%s answered something unexpected: %v", c.Command, err)}
	}
	return out, nil
}

// Watchers is every watcher (`watchers list`).
func (c CLI) Watchers() ([]WatcherInfo, error) {
	var out []WatcherInfo
	return out, c.call(&out, "watchers", "list")
}

// Watcher is one watcher in full (`watchers show`).
func (c CLI) Watcher(name string) (WatcherDetail, error) {
	var out WatcherDetail
	return out, c.call(&out, "watchers", "show", name)
}

// WatcherTrustCheck is what trusting a watcher approves, trusting nothing
// (`watchers trust --check`).
func (c CLI) WatcherTrustCheck(name string) (WatcherTrustOutput, error) {
	var out WatcherTrustOutput
	return out, c.call(&out, "watchers", "trust", name, "--check")
}

// TrustWatcher trusts a watcher after its review (`watchers trust --yes`).
func (c CLI) TrustWatcher(name string) error {
	var out WatcherTrustOutput
	return c.call(&out, "watchers", "trust", name, "--yes")
}

// SetWatcherPaused pauses a watcher (it stops), or resumes it.
func (c CLI) SetWatcherPaused(name string, paused bool) error {
	verb := "resume"
	if paused {
		verb = "pause"
	}
	var out map[string]any
	return c.call(&out, "watchers", verb, name)
}

// DeleteWatcher deletes a watcher; the hooks listening stay (`watchers delete`).
func (c CLI) DeleteWatcher(name string) error {
	var out map[string]any
	return c.call(&out, "watchers", "delete", name)
}
