package watchers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/BorysSerbyn/process-automator/core/internal/programs"
)

// Event is one thing that happened, as a watcher prints it on a line of its
// own, and as a hook's action gets it.
type Event struct {
	// ID identifies it among its watcher's events: one seen before is
	// dropped (a watcher may print an event again; it acts once).
	ID string `json:"id"`
	// Type is what happened (meeting.started, email.received): hooks listen
	// for a type.
	Type string `json:"type"`
	// At is when it happened (RFC 3339); when the watcher gives none, when it
	// was printed. A hook drops an event older than its staleness.
	At time.Time `json:"at"`
	// Watcher is the watcher it came from (set by the app).
	Watcher string `json:"watcher,omitempty"`
	// Data is what the watcher says about it: outside data, never
	// instructions.
	Data map[string]any `json:"data,omitempty"`
}

// What a watcher is told when it starts, as environment variables.
const (
	// CursorEnv is the cursor it last printed (empty the first time): where
	// to carry on from.
	CursorEnv = "PA_CURSOR"
	// NameEnv is the watcher's name.
	NameEnv = "PA_WATCHER"
)

// Limits on what a watcher prints.
const (
	MaxIDLength   = 256
	MaxLine       = 1 << 20  // one event's line
	MaxPollOutput = 16 << 20 // everything one poll prints
	MaxCursor     = 4 << 10
)

var typeRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,99}$`)

// ValidType reports whether s can be an event's type: letters, digits and
// . _ : / - (up to 100), starting with a letter or digit.
func ValidType(s string) bool { return typeRE.MatchString(s) }

// line is a line as a watcher writes it.
type line struct {
	ID     *string        `json:"id"`
	Type   string         `json:"type"`
	At     string         `json:"at"`
	Data   map[string]any `json:"data"`
	Cursor *string        `json:"cursor"`
}

// ParseLine reads one line a watcher printed: an event, or its cursor
// ({"cursor": "..."}); a blank line is neither. now is when it was printed,
// an event's time when it gives none.
func ParseLine(raw []byte, now time.Time) (ev *Event, cursor *string, err error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, nil, nil
	}
	var l line
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&l); err != nil {
		return nil, nil, fmt.Errorf("not an event or a cursor (a JSON object with id and type, or cursor): %w", err)
	}
	if l.Cursor != nil {
		if l.ID != nil || l.Type != "" || l.At != "" || l.Data != nil {
			return nil, nil, errors.New("a cursor line has only cursor")
		}
		if len(*l.Cursor) > MaxCursor {
			return nil, nil, fmt.Errorf("its cursor is longer than %d bytes", MaxCursor)
		}
		return nil, l.Cursor, nil
	}
	e := Event{Type: l.Type, Data: l.Data, At: now}
	if l.ID != nil {
		e.ID = *l.ID
	}
	if l.At != "" {
		if e.At, err = time.Parse(time.RFC3339, l.At); err != nil {
			return nil, nil, fmt.Errorf("at %q: an RFC 3339 time, like 2026-11-20T09:20:00+01:00", l.At)
		}
	}
	if err := e.Check(); err != nil {
		return nil, nil, err
	}
	return &e, nil, nil
}

// Check says what's wrong with an event's id or type, if anything.
func (e Event) Check() error {
	switch {
	case e.ID == "":
		return errors.New("an event needs an id")
	case len(e.ID) > MaxIDLength:
		return fmt.Errorf("its id is longer than %d bytes", MaxIDLength)
	case strings.IndexFunc(e.ID, unicode.IsControl) >= 0:
		return errors.New("its id has a control character")
	case !ValidType(e.Type):
		return fmt.Errorf("its type %q: letters, digits and . _ : / - (up to 100), like meeting.started", e.Type)
	}
	return nil
}

// ParseEvent reads one event given by hand (a sample for `hooks fire`): its
// line as a watcher would print it, indented or not.
func ParseEvent(raw []byte, now time.Time) (Event, error) {
	var compact bytes.Buffer
	if err := json.Compact(&compact, bytes.TrimSpace(raw)); err != nil {
		return Event{}, fmt.Errorf("the event isn't JSON: %w", err)
	}
	ev, cursor, err := ParseLine(compact.Bytes(), now)
	switch {
	case err != nil:
		return Event{}, err
	case cursor != nil || ev == nil:
		return Event{}, errors.New("give an event: a JSON object with id, type and optionally at and data")
	}
	return *ev, nil
}

// Map is the event as JSON data.
func (e Event) Map() map[string]any {
	m := map[string]any{"id": e.ID, "type": e.Type, "at": e.At.Format(time.RFC3339)}
	if e.Watcher != "" {
		m["watcher"] = e.Watcher
	}
	if e.Data != nil {
		m["data"] = e.Data
	}
	return m
}

// FromMap is an event from its JSON data (Map's).
func FromMap(m map[string]any) (Event, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return Event{}, err
	}
	var e Event
	err = json.Unmarshal(b, &e)
	return e, err
}

// Poll runs a poll watcher once (inv is its executable, arguments,
// environment and folder; CursorEnv is added) and returns what it printed:
// its events and its new cursor (the old one when it printed none). A poll
// that fails (exits non-zero, prints a line that's neither, or prints too
// much) returns nothing: its cursor stays where it was, so the next poll
// gets the same events again. An event of a type it doesn't declare is
// dropped and logged; the poll still works.
func Poll(ctx context.Context, w Watcher, inv programs.Invocation, cursor string) ([]Event, string, error) {
	ctx, cancel := context.WithTimeout(ctx, w.Timeout)
	defer cancel()
	out := &limited{max: MaxPollOutput}
	inv.Env = append(inv.Env, CursorEnv+"="+cursor, NameEnv+"="+w.Name)
	inv.Stdout = out
	_, err := programs.Exec(ctx, inv)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, cursor, fmt.Errorf("its poll took longer than %s (its timeout)", w.Timeout)
	}
	if err != nil {
		return nil, cursor, err
	}
	if out.over {
		return nil, cursor, fmt.Errorf("it printed more than %d MiB in one poll", MaxPollOutput>>20)
	}
	now := time.Now()
	var events []Event
	next := cursor
	undeclared := undeclared{w: w, log: inv.Log}
	for i, raw := range bytes.Split(out.buf.Bytes(), []byte("\n")) {
		ev, c, err := ParseLine(raw, now)
		if err != nil {
			return nil, cursor, fmt.Errorf("line %d: %w", i+1, err)
		}
		if c != nil {
			next = *c
		}
		if ev != nil && undeclared.keep(*ev) {
			ev.Watcher = w.Name
			events = append(events, *ev)
		}
	}
	return events, next, nil
}

// Stream runs a stream watcher until it ends or ctx does: each event it
// prints goes to event, each cursor to setCursor, as they come. A line that's
// neither is logged and skipped, as is an event of a type it doesn't declare. It returns why it ended (nil: it exited 0,
// or ctx ended).
func Stream(ctx context.Context, w Watcher, inv programs.Invocation, cursor string, event func(Event), setCursor func(string)) error {
	undeclared := undeclared{w: w, log: inv.Log}
	lw := &lineWriter{fn: func(raw []byte) {
		ev, c, err := ParseLine(raw, time.Now())
		switch {
		case err != nil:
			if inv.Log != nil {
				fmt.Fprintf(inv.Log, "skipped a line it printed: %v\n", err)
			}
		case c != nil:
			setCursor(*c)
		case ev != nil && undeclared.keep(*ev):
			ev.Watcher = w.Name
			event(*ev)
		}
	}, log: inv.Log}
	inv.Env = append(inv.Env, CursorEnv+"="+cursor, NameEnv+"="+w.Name)
	inv.Stdout = lw
	_, err := programs.Exec(ctx, inv)
	lw.flush()
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// undeclared drops the events of a type the watcher doesn't declare,
// logging each type once (per poll, or per start of a stream): a watcher
// printing a type over and over would otherwise fill the log.
type undeclared struct {
	w      Watcher
	log    io.Writer
	logged map[string]bool
}

// keep reports whether ev is of a type its watcher declares.
func (u *undeclared) keep(ev Event) bool {
	if u.w.Declares(ev.Type) {
		return true
	}
	if u.log != nil && !u.logged[ev.Type] {
		if u.logged == nil {
			u.logged = map[string]bool{}
		}
		u.logged[ev.Type] = true
		fmt.Fprintf(u.log, "dropped its %s events: %s doesn't declare that type (it declares %s)\n",
			ev.Type, DescriptionFile, strings.Join(u.w.Types(), ", "))
	}
	return false
}

// limited keeps up to max bytes, and whether more came.
type limited struct {
	buf  bytes.Buffer
	max  int
	over bool
}

func (l *limited) Write(p []byte) (int, error) {
	if room := l.max - l.buf.Len(); room < len(p) {
		l.over = true
		if room > 0 {
			l.buf.Write(p[:room])
		}
		return len(p), nil
	}
	return l.buf.Write(p)
}

// lineWriter hands each complete line to fn; a line longer than MaxLine is
// dropped, saying so.
type lineWriter struct {
	mu   sync.Mutex
	buf  []byte
	skip bool
	fn   func([]byte)
	log  io.Writer
}

func (l *lineWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			l.add(p)
			break
		}
		l.add(p[:i])
		if !l.skip {
			l.fn(l.buf)
		}
		l.buf, l.skip = l.buf[:0], false
		p = p[i+1:]
	}
	return n, nil
}

func (l *lineWriter) add(p []byte) {
	if l.skip {
		return
	}
	if len(l.buf)+len(p) > MaxLine {
		l.skip, l.buf = true, l.buf[:0]
		if l.log != nil {
			fmt.Fprintf(l.log, "skipped a line longer than %d KiB\n", MaxLine>>10)
		}
		return
	}
	l.buf = append(l.buf, p...)
}

// flush hands over a last line without a newline.
func (l *lineWriter) flush() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.buf) > 0 && !l.skip {
		l.fn(l.buf)
	}
	l.buf = nil
}
