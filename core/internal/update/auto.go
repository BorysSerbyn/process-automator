package update

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/atomicfile"
)

// Automatic updates: the scheduler checks for a newer release every
// CheckEvery and, while they're on, installs it as soon as no routine or hook
// is running (Decide).
// What it found, and the automatic install it last started, are kept in a
// file between its passes and across restarts (AutoState).

// Automatic updates' timings.
const (
	// CheckEvery is how often the scheduler asks for the latest release.
	CheckEvery = time.Hour
	// RetryAfter is how long after an automatic install of a release was
	// started it may be started again (it failed: one that worked makes it
	// the running version).
	RetryAfter = 24 * time.Hour
)

// AutoState is what automatic updates keep (update-auto.json): the last
// check's answer and the automatic install last started.
type AutoState struct {
	// CheckedAt is when the latest release was last asked for, by the
	// scheduler or `update --check`; CheckError why that failed.
	CheckedAt  *time.Time `json:"checkedAt,omitempty"`
	CheckError string     `json:"checkError,omitempty"`
	// Latest is the newest release the last check that worked found, and
	// Downloadable whether it has a download for this machine (else only a
	// build from the source checkout installs it).
	Latest       string `json:"latest,omitempty"`
	Downloadable bool   `json:"downloadable,omitempty"`
	// Tried is the release an automatic install was last started for, and
	// TriedAt when.
	Tried   string     `json:"tried,omitempty"`
	TriedAt *time.Time `json:"triedAt,omitempty"`
	// Waiting is why the scheduler's last pass didn't install the newer
	// release (AutoPlan.Waiting).
	Waiting string `json:"waiting,omitempty"`
}

// LoadAuto reads the state; a missing file is an empty state.
func LoadAuto(path string) (AutoState, error) {
	var st AutoState
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	return st, json.Unmarshal(b, &st)
}

func changeAuto(path string, change func(*AutoState)) error {
	return atomicfile.UpdateJSON(path, func(p string) (*AutoState, error) {
		st, err := LoadAuto(p)
		return &st, err
	}, func(st *AutoState) error { change(st); return nil })
}

// RecordCheck records a check's answer: the latest release (tag, "" when
// there are none) and whether it has a download for this machine, or why
// the check failed (the last answer that worked is kept).
func RecordCheck(path, tag string, downloadable bool, checkErr error, now time.Time) error {
	return changeAuto(path, func(st *AutoState) {
		st.CheckedAt, st.CheckError = &now, ""
		if checkErr != nil {
			st.CheckError = checkErr.Error()
			return
		}
		st.Latest, st.Downloadable = tag, downloadable
	})
}

// RecordTry records an automatic install of tag starting.
func RecordTry(path, tag string, now time.Time) error {
	return changeAuto(path, func(st *AutoState) { st.Tried, st.TriedAt, st.Waiting = tag, &now, "" })
}

// RecordWaiting records why the newer release isn't installing yet.
func RecordWaiting(path, waiting string) error {
	return changeAuto(path, func(st *AutoState) { st.Waiting = waiting })
}

// Facts are what Decide weighs besides the state, gathered by the scheduler.
type Facts struct {
	Now time.Time
	// Enabled is the update.auto setting.
	Enabled bool
	// Current is the running version.
	Current string
	// Blocked says why this install can't be updated automatically (a
	// development build, one Homebrew's formula installed); "" when it can.
	Blocked string
	// Installing is true while an update installs.
	Installing bool
	// Busy says what's running ("standup is running"); "" when nothing is.
	Busy string
	// When says a time in words ("tomorrow 09:20").
	When func(t, now time.Time) string
}

// AutoPlan is what to do now.
type AutoPlan struct {
	// Check is true when it's time to ask for the latest release.
	Check bool
	// Install is the release to install now; "" for none.
	Install string
	// Waiting says, in words, why the newer release isn't installing yet, or
	// won't by itself; "" when there's none, it's installing now, or
	// automatic updates are off.
	Waiting string
}

// Decide is what automatic updates do now: check when CheckEvery has passed
// since the last check (whether or not they're on, so a newer release is
// known), and install the newest release as soon as no routine or hook is
// running: they're on, this install can be updated, the release has a
// download for this machine and wasn't tried in the last RetryAfter.
// Nothing else holds it back.
func Decide(st AutoState, f Facts) AutoPlan {
	var plan AutoPlan
	plan.Check = st.CheckedAt == nil || f.Now.Sub(*st.CheckedAt) >= CheckEvery || f.Now.Before(*st.CheckedAt)
	if !f.Enabled || f.Blocked != "" || f.Installing || !Newer(f.Current, st.Latest) {
		return plan
	}
	tag := st.Latest
	switch {
	case !st.Downloadable:
		plan.Waiting = tag + " has no download for this computer, so it isn't installed automatically"
	case st.Tried == tag && st.TriedAt != nil && f.Now.Sub(*st.TriedAt) < RetryAfter:
		plan.Waiting = "The automatic update to " + tag + " didn't finish; it's tried again after " + f.When(st.TriedAt.Add(RetryAfter), f.Now)
	case f.Busy != "":
		plan.Waiting = tag + " installs automatically once nothing is running (" + f.Busy + ")"
	default:
		plan.Install = tag
	}
	return plan
}
