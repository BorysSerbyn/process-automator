// Package notifications keeps the app's own notifications (runs that
// failed, needed a sign-in, were skipped or missed; updates) for frontends
// to deliver. The core only records them; frontends ask for the pending ones
// (`procauto notifications list --pending --json`), show them (automator-ui
// as desktop notifications), mark them delivered, and on a click run
// `procauto notifications open <id>`, which says what to show: the run it's
// about, in its routine's window, or the app.
//
// It also keeps the notifications a routine or hook asked the app for in
// its run (kind run: a plugin's, through pluginapi.Host.Notify, or a
// program's, through $PA_NOTIFY): one may carry a link, which a click opens,
// and one may wait until I acknowledge it (Acknowledge), never pruned while
// it waits.
package notifications

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"slices"
	"strings"
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
	// KindRun is a routine's or hook's own, asked for in its run (a plugin,
	// or a program through $PA_NOTIFY): it may carry a link (URL) or wait
	// for me to acknowledge it (Acknowledge).
	KindRun = "run"
	// kindAcknowledge is KindRun's name in earlier builds: read as KindRun.
	kindAcknowledge = "acknowledge"
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
	// or missed), "app" (about the app, e.g. an update) or "run" (a
	// routine's or hook's own, asked for in its run: a plugin's, such as
	// notify_me's, or a program's through $PA_NOTIFY).
	Kind  string `json:"kind" jsonschema:"enum=failure,enum=app,enum=run"`
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	// Routine and Run (a run ID, as in `runs show`) are what it's about, if any.
	Routine string `json:"routine,omitempty"`
	Run     string `json:"run,omitempty"`
	// Hook is true when Routine names a hook, not a routine.
	Hook bool `json:"hook,omitempty"`
	// Update is true when it's about an update (one installed, or failed):
	// a click shows the app's updates.
	Update bool `json:"update,omitempty"`
	// URL is a link (http or https) a click on it opens (`notifications
	// open`: open-url); only kind run has one.
	URL string `json:"url,omitempty"`
	// Urgent breaks through focus modes where the OS allows (macOS
	// time-sensitive, Linux critical urgency).
	Urgent bool `json:"urgent,omitempty"`
	// DeliveredAt is when a frontend showed it; absent until then.
	DeliveredAt *time.Time `json:"deliveredAt,omitempty"`
	// Acknowledge is true when it waits for me to acknowledge it (only kind
	// run may): frontends show it with an Acknowledge action that runs
	// `notifications ack <id>`, and health is error until then.
	Acknowledge bool `json:"acknowledge,omitempty"`
	// AcknowledgedAt is when I acknowledged it; absent while it waits.
	AcknowledgedAt *time.Time `json:"acknowledgedAt,omitempty"`
}

// Waiting is whether it waits for me to acknowledge it.
func (n Notification) Waiting() bool { return n.Acknowledge && n.AcknowledgedAt == nil }

// Add records a notification (its ID and time are set here) and returns it.
// Only one of kind run may wait to be acknowledged or carry a link, and it
// must be Valid.
func Add(path string, n Notification, now time.Time) (Notification, error) {
	if n.Kind == KindRun {
		if err := Valid(n.Title, n.Body, n.URL, n.Acknowledge); err != nil {
			return Notification{}, err
		}
	} else {
		n.Acknowledge, n.URL = false, ""
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	n.ID, n.At = now.UTC().Format("20060102T150405")+"-"+hex.EncodeToString(b), now
	n.AcknowledgedAt = nil
	err := update(path, func(f *File) {
		f.Notifications = prune(append(f.Notifications, n), now)
	})
	return n, err
}

// Valid says what's wrong with a notification a run asks for, if anything:
// it needs a title, a body when it's to be acknowledged (it says what to act
// on), and a link, if any, that's http or https.
func Valid(title, body, link string, acknowledge bool) error {
	if strings.TrimSpace(title) == "" {
		return errors.New("title is required")
	}
	if acknowledge && strings.TrimSpace(body) == "" {
		return errors.New("body is required with acknowledge")
	}
	if link == "" {
		return nil
	}
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("url %q isn't an http or https link", link)
	}
	return nil
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

// Acknowledged are the notifications I've acknowledged, the last
// acknowledged last (pruned as the others are: the newest kept, none older
// than keepFor).
func (f *File) Acknowledged() []Notification {
	out := []Notification{}
	for _, n := range f.Notifications {
		if n.Acknowledge && n.AcknowledgedAt != nil {
			out = append(out, n)
		}
	}
	slices.SortStableFunc(out, func(a, b Notification) int { return a.AcknowledgedAt.Compare(*b.AcknowledgedAt) })
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
		if n.Kind == kindAcknowledge {
			n.Kind = KindRun
		}
		if n.Kind == KindFailure || n.Kind == KindApp || n.Kind == KindRun {
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
