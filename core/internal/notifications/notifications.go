// Package notifications keeps the app's own notifications (runs that
// failed, needed a sign-in, were skipped or missed; updates) for frontends
// to deliver. The core only records them; frontends ask for the pending ones
// (`procauto notifications list --pending --json`), show them (automator-ui
// as desktop notifications), mark them delivered, and on a click run
// `procauto notifications open <id>`, which says what to show: the run it's
// about, in its routine's window, or the app.
//
// It also keeps the notifications a plugin asked the app for in a run
// (pluginapi.Host.AskToAcknowledge): each waits until I acknowledge it
// (Acknowledge), and is never pruned while it waits.
package notifications

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/atomicfile"
)

// SchemaVersion is the notifications file's format version.
const SchemaVersion = 1

// Kinds of notification.
const (
	// KindFailure is a run that failed, needs a sign-in, or was skipped or missed.
	KindFailure = "failure"
	// KindApp is about the app itself (an update installed, or failed).
	KindApp = "app"
	// KindAcknowledge is a routine's or hook's, asked for by a plugin in its
	// run, that waits for me to acknowledge it.
	KindAcknowledge = "acknowledge"
)

// Kept: the newest keep notifications, none older than keepFor.
const (
	keep    = 200
	keepFor = 30 * 24 * time.Hour
)

// File is the notifications file: written and read by the core only.
type File struct {
	SchemaVersion int            `json:"schemaVersion"`
	Notifications []Notification `json:"notifications"`
}

// Notification is one thing to tell me.
type Notification struct {
	ID string    `json:"id"`
	At time.Time `json:"at"`
	// Kind is "failure" (a run that failed, needs a sign-in, or was skipped
	// or missed), "app" (about the app, e.g. an update) or "acknowledge" (a
	// routine's or hook's, through a plugin, waiting to be acknowledged).
	Kind  string `json:"kind" jsonschema:"enum=failure,enum=app,enum=acknowledge"`
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	// Routine and Run (a run ID, as in `runs show`) are what it's about, if any.
	Routine string `json:"routine,omitempty"`
	Run     string `json:"run,omitempty"`
	// Hook is true when Routine names a hook, not a routine.
	Hook bool `json:"hook,omitempty"`
	// Urgent breaks through focus modes where the OS allows (macOS
	// time-sensitive, Linux critical urgency).
	Urgent bool `json:"urgent,omitempty"`
	// DeliveredAt is when a frontend showed it; absent until then.
	DeliveredAt *time.Time `json:"deliveredAt,omitempty"`
	// Acknowledge is true when it waits for me to acknowledge it (kind
	// acknowledge): frontends show it with an Acknowledge action that runs
	// `notifications ack <id>`, and health is error until then.
	Acknowledge bool `json:"acknowledge,omitempty"`
	// AcknowledgedAt is when I acknowledged it; absent while it waits.
	AcknowledgedAt *time.Time `json:"acknowledgedAt,omitempty"`
}

// Waiting is whether it waits for me to acknowledge it.
func (n Notification) Waiting() bool { return n.Acknowledge && n.AcknowledgedAt == nil }

// Add records a notification (its ID and time are set here) and returns it.
// A notification of kind acknowledge is recorded waiting.
func Add(path string, n Notification, now time.Time) (Notification, error) {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	n.ID, n.At = now.UTC().Format("20060102T150405")+"-"+hex.EncodeToString(b), now
	n.Acknowledge, n.AcknowledgedAt = n.Kind == KindAcknowledge, nil
	err := update(path, func(f *File) {
		f.Notifications = prune(append(f.Notifications, n), now)
	})
	return n, err
}

// prune keeps the newest keep notifications, none older than keepFor,
// besides every one still waiting.
func prune(list []Notification, now time.Time) []Notification {
	cutoff := now.Add(-keepFor)
	others := 0
	for _, x := range list {
		if !x.Waiting() && x.At.After(cutoff) {
			others++
		}
	}
	kept := list[:0]
	for _, x := range list {
		switch {
		case x.Waiting():
			kept = append(kept, x)
		case !x.At.After(cutoff):
		case others > keep:
			others--
		default:
			kept = append(kept, x)
		}
	}
	return kept
}

// Waiting are the notifications waiting for me to acknowledge them, oldest
// first.
func (f *File) Waiting() []Notification {
	out := []Notification{}
	for _, n := range f.Notifications {
		if n.Waiting() {
			out = append(out, n)
		}
	}
	return out
}

// Acknowledge acknowledges the notifications named (all: every one
// waiting) and returns those it acknowledged. Naming one that isn't
// waiting (acknowledged already, or one that never waits) acknowledges
// nothing more; naming one that doesn't exist is an error, and nothing is
// acknowledged.
func Acknowledge(path string, ids []string, all bool, now time.Time) ([]Notification, error) {
	done := []Notification{}
	err := atomicfile.UpdateJSON(path, Load, func(f *File) error {
		want := map[string]bool{}
		for _, id := range ids {
			want[id] = true
		}
		for i := range f.Notifications {
			n := &f.Notifications[i]
			if !want[n.ID] && !all {
				continue
			}
			delete(want, n.ID)
			if n.Waiting() {
				t := now
				n.AcknowledgedAt = &t
				done = append(done, *n)
			}
		}
		for _, id := range ids {
			if want[id] {
				return fmt.Errorf("no notification %q", id)
			}
		}
		f.SchemaVersion = SchemaVersion
		return nil
	})
	if err != nil {
		return nil, err
	}
	return done, nil
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
	// Older versions also kept another kind ("routine"): it's dropped,
	// and gone from the file at the next write.
	kept := []Notification{}
	for _, n := range f.Notifications {
		if n.Kind == KindFailure || n.Kind == KindApp || n.Kind == KindAcknowledge {
			kept = append(kept, n)
		}
	}
	f.Notifications = kept
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
