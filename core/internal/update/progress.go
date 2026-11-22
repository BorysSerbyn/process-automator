package update

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/atomicfile"
	"github.com/BorysSerbyn/process-automator/core/internal/filelock"
)

// Progress is how the latest update went, or is going: kept in a file by
// the process installing it, so others (`update progress`) can say what
// it's doing.
type Progress struct {
	// Tag is the release being installed, once known.
	Tag string `json:"tag,omitempty"`
	// Step is what it's doing, in words.
	Step string `json:"step,omitempty"`
	// Detail is the latest line the step printed (a build's output).
	Detail    string    `json:"detail,omitempty"`
	StartedAt time.Time `json:"startedAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// FinishedAt is set when it's over, and Error when it failed.
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Error      string     `json:"error,omitempty"`
}

// ReadProgress reads the progress file; ok is false when there's none.
func ReadProgress(path string) (p Progress, ok bool, err error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Progress{}, false, nil
	}
	if err != nil {
		return Progress{}, false, err
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return Progress{}, false, err
	}
	return p, true, nil
}

// Where the latest update stands (Latest).
const (
	StateNone       = "none"
	StateInstalling = "installing"
	StateDone       = "done"
	StateFailed     = "failed"
)

// startGrace is how long an update that hasn't finished counts as
// installing without holding the lock: a detached one between `update
// --detach` and the update it starts taking the lock.
const startGrace = 30 * time.Second

// Installing reports whether an update is installing: its lock is held.
func Installing(lockPath string) bool {
	unlock, err := filelock.Lock(lockPath, false)
	if err != nil {
		return errors.Is(err, filelock.ErrHeld)
	}
	unlock()
	return false
}

// Latest is where the latest update stands, from its progress file and its
// lock, and its progress: while it's installing, FinishedAt and Error are
// unset; one that stopped before it finished failed, its log (logPath) says
// why.
func Latest(progressPath, lockPath, logPath string, now time.Time) (state string, p Progress, err error) {
	p, ok, err := ReadProgress(progressPath)
	if err != nil {
		return StateNone, Progress{}, err
	}
	held := Installing(lockPath)
	switch {
	case !ok && held:
		return StateInstalling, p, nil
	case !ok:
		return StateNone, p, nil
	case held || (p.FinishedAt == nil && now.Sub(p.UpdatedAt) < startGrace):
		p.FinishedAt, p.Error = nil, ""
		return StateInstalling, p, nil
	case p.FinishedAt == nil:
		p.Error = "it stopped before it finished (log: " + logPath + ")"
		return StateFailed, p, nil
	case p.Error != "":
		return StateFailed, p, nil
	}
	return StateDone, p, nil
}

// detailEvery is how often at most a step's output updates the file.
const detailEvery = time.Second

// maxDetail caps Detail, in runes.
const maxDetail = 200

// Reporter records an update's progress in its file as it goes. It's an
// io.Writer too: what a step prints becomes its Detail (its latest line).
// Recording is best effort: an update never fails for it.
type Reporter struct {
	path    string
	mu      sync.Mutex
	p       Progress
	partial []byte
	written time.Time
	now     func() time.Time
}

// NewReporter starts recording a new update's progress at path.
func NewReporter(path, step string) *Reporter {
	r := &Reporter{path: path, now: time.Now}
	now := r.now()
	r.p = Progress{Step: step, StartedAt: now, UpdatedAt: now}
	r.save()
	return r
}

// Tag records the release being installed.
func (r *Reporter) Tag(tag string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.p.Tag = tag
	r.save()
}

// Step records what the update does now.
func (r *Reporter) Step(step string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.p.Step, r.p.Detail, r.partial = step, "", nil
	r.save()
}

// Finish records the update's end: its error, or its last step.
func (r *Reporter) Finish(step string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.p.FinishedAt = &now
	if err != nil {
		r.p.Error = err.Error()
	} else {
		r.p.Step, r.p.Detail = step, ""
	}
	r.save()
}

// Write takes a step's output; its latest non-empty line is the Detail.
func (r *Reporter) Write(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.partial = append(r.partial, b...)
	text := strings.ReplaceAll(string(r.partial), "\r", "\n")
	lines := strings.Split(text, "\n")
	r.partial = []byte(lines[len(lines)-1])
	for i := len(lines) - 2; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			if runes := []rune(line); len(runes) > maxDetail {
				line = string(runes[:maxDetail]) + "…"
			}
			r.p.Detail = line
			break
		}
	}
	if r.now().Sub(r.written) >= detailEvery {
		r.save()
	}
	return len(b), nil
}

// save writes the file; r.mu is held.
func (r *Reporter) save() {
	if r.path == "" {
		return
	}
	r.p.UpdatedAt = r.now()
	r.written = r.p.UpdatedAt
	_ = atomicfile.WriteJSON(r.path, r.p)
}
