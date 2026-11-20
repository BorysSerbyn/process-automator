// Package notify keeps the notifications the core records for frontends to
// deliver: what routines ask to tell me (notify_me) and runs that failed. The
// core only records them; frontends show them (automator-ui as desktop
// notifications), mark them delivered, and on a click run `procauto
// notifications open <id>`, which carries out the click action.
package notify

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/atomicfile"
)

// SchemaVersion is the notifications file's format version.
const SchemaVersion = 1

// Kinds of notification.
const (
	// KindRoutine is what a routine asked to tell me (notify_me).
	KindRoutine = "routine"
	// KindFailure is a run that failed, needs a sign-in, or was missed.
	KindFailure = "failure"
	// KindApp is about the app itself (an update installed, or failed).
	KindApp = "app"
)

// Kept: the newest keep notifications, none older than keepFor.
const (
	keep    = 200
	keepFor = 30 * 24 * time.Hour
)

// File is the notifications file: written by the core, read by frontends.
type File struct {
	SchemaVersion int            `json:"schemaVersion"`
	Notifications []Notification `json:"notifications"`
}

// Notification is one thing to tell me.
type Notification struct {
	ID string    `json:"id"`
	At time.Time `json:"at"`
	// Kind is "routine" (a routine's notify_me), "failure" (a run that failed,
	// needs a sign-in, or was missed) or "app" (about the app, e.g. an update).
	Kind  string `json:"kind" jsonschema:"enum=routine,enum=failure,enum=app"`
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	// Urgent asks frontends to break through focus modes where the OS allows
	// (macOS time-sensitive, Linux critical urgency).
	Urgent bool `json:"urgent,omitempty"`
	// Routine and Run (a run ID, as in `runs show`) are what it's about, if any.
	Routine string `json:"routine,omitempty"`
	Run     string `json:"run,omitempty"`
	// Open is a file the routine asked to open on click (instead of the
	// configured click action).
	Open string `json:"open,omitempty"`
	// DeliveredAt is when a frontend showed it; absent until then.
	DeliveredAt *time.Time `json:"deliveredAt,omitempty"`
}

// Add records a notification (its ID and time are set here) and returns it.
func Add(path string, n Notification, now time.Time) (Notification, error) {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	n.ID, n.At = now.UTC().Format("20060102T150405")+"-"+hex.EncodeToString(b), now
	err := update(path, func(f *File) {
		f.Notifications = append(f.Notifications, n)
		cutoff := now.Add(-keepFor)
		kept := f.Notifications[:0]
		for _, x := range f.Notifications {
			if x.At.After(cutoff) {
				kept = append(kept, x)
			}
		}
		if len(kept) > keep {
			kept = kept[len(kept)-keep:]
		}
		f.Notifications = kept
	})
	return n, err
}

// MarkDelivered records that a frontend showed these notifications.
func MarkDelivered(path string, ids []string, now time.Time) error {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	return update(path, func(f *File) {
		for i := range f.Notifications {
			if want[f.Notifications[i].ID] && f.Notifications[i].DeliveredAt == nil {
				t := now
				f.Notifications[i].DeliveredAt = &t
			}
		}
	})
}

// Load reads the file; a missing one is empty.
func Load(path string) (*File, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &File{SchemaVersion: SchemaVersion, Notifications: []Notification{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if f.Notifications == nil {
		f.Notifications = []Notification{}
	}
	return &f, nil
}

// Get finds one notification by ID.
func Get(path, id string) (Notification, error) {
	f, err := Load(path)
	if err != nil {
		return Notification{}, err
	}
	for _, n := range f.Notifications {
		if n.ID == id {
			return n, nil
		}
	}
	return Notification{}, fmt.Errorf("no notification %q", id)
}

// update applies fn under a cross-process lock and writes the file atomically.
func update(path string, fn func(*File)) error {
	return atomicfile.UpdateJSON(path, Load, func(f *File) error {
		fn(f)
		f.SchemaVersion = SchemaVersion
		return nil
	})
}

// Click actions: what clicking a notification does.
const (
	// ActionShowRun shows the run the notification is about (a frontend's
	// routines page; without one, a readable report of the run).
	ActionShowRun = "show-run"
	// ActionOpen opens a file, folder or URL with its default app: "open <target>".
	ActionOpen = "open"
	// ActionRun runs a shell command: "run <command line>".
	ActionRun = "run"
	// ActionNone does nothing.
	ActionNone = "none"
)

// Action is a parsed click action.
type Action struct {
	Kind   string // one of the Action* constants
	Target string // what to open, or the command line to run
}

// ParseAction parses a click action: "show-run", "open <file, folder or URL>",
// "run <shell command>" or "none".
func ParseAction(s string) (Action, error) {
	s = strings.TrimSpace(s)
	kind, rest, _ := strings.Cut(s, " ")
	rest = strings.TrimSpace(rest)
	switch kind {
	case ActionShowRun, ActionNone:
		if rest != "" {
			return Action{}, fmt.Errorf("click action %q takes nothing after %s", s, kind)
		}
		return Action{Kind: kind}, nil
	case ActionOpen, ActionRun:
		if rest == "" {
			return Action{}, fmt.Errorf("click action %q: say what to %s", s, kind)
		}
		return Action{Kind: kind, Target: rest}, nil
	}
	return Action{}, fmt.Errorf("click action %q: want show-run, open <file or URL>, run <command> or none", s)
}
