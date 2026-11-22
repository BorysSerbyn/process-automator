// Package permissions names what the OS must allow before something can
// work (macOS's privacy permissions, and their Linux counterpart where there
// is one), and keeps what the scheduler last found for each
// (permissions.json). Only what runs code declares one: a plugin, a program,
// a program hook, a watcher. A prompt needs those of the plugins it uses.
//
// The OS is asked by sysenv, from the scheduler: macOS answers for the
// process that launched the one asking (a terminal, for a command typed in
// one), and the scheduler is launched from the app, so its answer is the
// app's. Everything else reads what it recorded.
package permissions

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/atomicfile"
)

// Info is one permission: its name, as files write it, and what the OS
// calls it.
type Info struct {
	Name, Label string
	// Means is what it allows, in a few words, for the guide.
	Means string
}

// All is every permission something can declare.
var All = []Info{
	{"input-monitoring", "Input Monitoring", "reading the keyboard and mouse (a key-press watcher); on Linux, reading /dev/input"},
	{"accessibility", "Accessibility", "controlling other apps and reading their windows"},
	{"screen-recording", "Screen Recording", "seeing the screen"},
	{"notifications", "Notifications", "showing desktop notifications"},
	{"full-disk-access", "Full Disk Access", "reading other apps' protected data (Mail, Messages, Safari)"},
	{"calendars", "Calendars", "reading or changing calendars"},
	{"contacts", "Contacts", "reading or changing contacts"},
	{"reminders", "Reminders", "reading or changing reminders"},
	{"microphone", "Microphone", "recording sound"},
	{"camera", "Camera", "using the camera"},
	{"automation", "Automation", "sending Apple Events to other apps (AppleScript)"},
}

// Names are every permission's name, in All's order.
func Names() []string {
	out := make([]string, len(All))
	for i, p := range All {
		out[i] = p.Name
	}
	return out
}

// Known reports whether name is a permission.
func Known(name string) bool {
	_, ok := find(name)
	return ok
}

// Label is what the OS calls a permission (its name, for one that isn't).
func Label(name string) string {
	if p, ok := find(name); ok {
		return p.Label
	}
	return name
}

func find(name string) (Info, bool) {
	for _, p := range All {
		if p.Name == name {
			return p, true
		}
	}
	return Info{}, false
}

// Check validates permissions as a file (what, for messages) declares
// them: each one known. It returns them sorted, each once.
func Check(what string, list []string) ([]string, error) {
	for _, p := range list {
		if !Known(p) {
			return nil, fmt.Errorf("%s: unknown permission %q (one of: %s)", what, p, strings.Join(Names(), ", "))
		}
	}
	return Sorted(list), nil
}

// Sorted is list sorted, each once (nil for none).
func Sorted(list []string) []string {
	if len(list) == 0 {
		return nil
	}
	out := slices.Clone(list)
	slices.Sort(out)
	return slices.Compact(out)
}

// Status is what the OS said about a permission.
type Status string

// Statuses. Only Missing keeps anything from running: a permission that
// can't be checked, or hasn't been yet, is never taken for one that's
// missing. (The scheduler checks every permission at the start of each pass,
// before it schedules anything or starts a watcher, so it never acts on one
// that's NotChecked; a CLI that can't check them doesn't turn health red.)
const (
	// Granted: the OS allows it.
	Granted Status = "granted"
	// Missing: the OS doesn't allow it: turned off, or never asked.
	Missing Status = "missing"
	// Unknown: it can't be checked here (no public way to ask, or not from
	// this build: outside the app's bundle, without cgo).
	Unknown Status = "unknown"
	// Unsupported: this OS has no such permission, or it isn't checked on it.
	Unsupported Status = "unsupported"
	// NotChecked: nothing has been recorded yet (the scheduler checks every
	// permission on each pass).
	NotChecked Status = "not-checked"
)

// Statuses are every status, for the contract.
var Statuses = []Status{Granted, Missing, Unknown, Unsupported, NotChecked}

// Blocks reports whether s keeps what needs the permission from running.
func (s Status) Blocks() bool { return s == Missing }

// State is what the scheduler last found (permissions.json).
type State struct {
	// CheckedAt is when it last checked.
	CheckedAt time.Time `json:"checkedAt"`
	// Permissions are each permission's status then.
	Permissions map[string]Status `json:"permissions"`
	// Requests are the latest request for each permission it took
	// (`permissions request`), by permission.
	Requests map[string]Request `json:"requests,omitempty"`
	// Needs are, by name, what each routine, program, hook and watcher
	// needed at that check, with each permission's status then (empty for
	// one that needs none): when one's differs at the next check, its
	// health is reset. Absent until the first check that records them.
	Needs map[string]map[string]Status `json:"needs,omitempty"`
}

// Changed are the names whose needs differ between before and after:
// another status for a permission, or another set of permissions. Names in
// only one of them (added, removed) haven't changed; with nothing before
// (the first check), nothing has.
func Changed(before, after map[string]map[string]Status) []string {
	var out []string
	if before == nil {
		return nil
	}
	for name, now := range after {
		was, ok := before[name]
		if ok && !maps.Equal(was, now) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// Request is how the scheduler took a request to ask the OS for a
// permission.
type Request struct {
	// At is when it took it.
	At time.Time `json:"at"`
	// Asked is whether it asked the OS; Why says why not.
	Asked bool   `json:"asked"`
	Why   string `json:"why,omitempty"`
}

// Of is what was recorded for name: NotChecked when nothing was.
func (s *State) Of(name string) Status {
	if s == nil {
		return NotChecked
	}
	if st, ok := s.Permissions[name]; ok && st != "" {
		return st
	}
	return NotChecked
}

// Load reads the recorded state: nil, with no error, when nothing has been
// recorded yet.
func Load(path string) (*State, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &s, nil
}

// Update changes the recorded state under its lock.
func Update(path string, change func(*State)) error {
	return atomicfile.UpdateJSON(path, func(path string) (*State, error) {
		s, _ := Load(path) // unreadable: start afresh, it's all checked again
		if s == nil {
			s = &State{}
		}
		return s, nil
	}, func(s *State) error {
		change(s)
		return nil
	})
}
