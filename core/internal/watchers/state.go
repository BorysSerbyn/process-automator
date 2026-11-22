package watchers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/atomicfile"
)

// State is what the daemon keeps of each watcher across restarts, so it
// never acts on an event twice: its cursor, the event IDs it has seen, and
// how it last went. The daemon is its one writer.
type State struct {
	Version  int                      `json:"version"`
	Watchers map[string]*WatcherState `json:"watchers"`
}

// WatcherState is one watcher's state.
type WatcherState struct {
	// Cursor is the cursor it last printed: it's given it when it starts.
	Cursor string `json:"cursor,omitempty"`
	// Seen are the IDs of events it printed, with when: one printed again
	// is dropped. Kept for SeenFor, and at most MaxSeen.
	Seen map[string]time.Time `json:"seen,omitempty"`
	// LastPollAt is when a poll last succeeded; StartedAt when a stream
	// last started.
	LastPollAt *time.Time `json:"lastPollAt,omitempty"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	// LastEventAt is when it last printed a new event.
	LastEventAt *time.Time `json:"lastEventAt,omitempty"`
	// Error is why its last poll failed, or its stream last ended (cleared
	// by the next poll that works, or a stream that keeps running, and when
	// a permission it needs changes); ErrorAt is when.
	Error   string     `json:"error,omitempty"`
	ErrorAt *time.Time `json:"errorAt,omitempty"`
}

// How long, and how many, seen event IDs are kept per watcher.
const (
	SeenFor = 7 * 24 * time.Hour
	MaxSeen = 5000
)

const stateVersion = 1

// LoadState reads the state file; a missing one is empty.
func LoadState(path string) (*State, error) {
	st := &State{Version: stateVersion, Watchers: map[string]*WatcherState{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, st); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if st.Version != stateVersion {
		return nil, fmt.Errorf("%s: unknown state version %d", path, st.Version)
	}
	if st.Watchers == nil {
		st.Watchers = map[string]*WatcherState{}
	}
	return st, nil
}

// Save writes the state file.
func (st *State) Save(path string) error { return atomicfile.WriteJSON(path, st) }

// Of is a watcher's state, made when it has none.
func (st *State) Of(name string) *WatcherState {
	w := st.Watchers[name]
	if w == nil {
		w = &WatcherState{}
		st.Watchers[name] = w
	}
	return w
}

// New keeps the events not seen before (nor twice in events), marking
// them seen at now, and prunes what's seen past SeenFor and MaxSeen.
func (w *WatcherState) New(events []Event, now time.Time) []Event {
	if w.Seen == nil {
		w.Seen = map[string]time.Time{}
	}
	var fresh []Event
	for _, e := range events {
		if _, ok := w.Seen[e.ID]; ok {
			continue
		}
		w.Seen[e.ID] = now
		fresh = append(fresh, e)
	}
	if len(fresh) > 0 {
		at := now
		w.LastEventAt = &at
	}
	for id, at := range w.Seen {
		if now.Sub(at) > SeenFor {
			delete(w.Seen, id)
		}
	}
	if over := len(w.Seen) - MaxSeen; over > 0 {
		ids := make([]string, 0, len(w.Seen))
		for id := range w.Seen {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return w.Seen[ids[i]].Before(w.Seen[ids[j]]) })
		for _, id := range ids[:over] {
			delete(w.Seen, id)
		}
	}
	return fresh
}

// Failed records why it last failed; Worked clears it.
func (w *WatcherState) Failed(err error, now time.Time) {
	at := now
	w.Error, w.ErrorAt = err.Error(), &at
}

// Worked records that it works again.
func (w *WatcherState) Worked() { w.Error, w.ErrorAt = "", nil }
