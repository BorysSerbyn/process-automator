// Package notifications keeps the app's own notifications (runs that
// failed, needed a sign-in, were skipped or missed; updates) for frontends
// to deliver. The core only records them; frontends ask for the pending ones
// (`procauto notifications list --pending --json`), show them (automator-ui
// as desktop notifications), mark them delivered, and on a click run
// `procauto notifications open <id>`, which says what to show: the run it's
// about, in its routine's window, or the app.
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
	// or missed) or "app" (about the app, e.g. an update).
	Kind  string `json:"kind" jsonschema:"enum=failure,enum=app"`
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	// Routine and Run (a run ID, as in `runs show`) are what it's about, if any.
	Routine string `json:"routine,omitempty"`
	Run     string `json:"run,omitempty"`
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
	// Older versions also kept another kind ("routine"): it's dropped,
	// and gone from the file at the next write.
	kept := []Notification{}
	for _, n := range f.Notifications {
		if n.Kind == KindFailure || n.Kind == KindApp {
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
