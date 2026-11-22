//go:build unix

package watchers

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/programs"
)

// script adds a watcher whose executable is a shell script with body; it
// returns it, as the app keeps it.
func script(t *testing.T, desc, body string) Watcher {
	t.Helper()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "watch"), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(src, DescriptionFile)
	if err := os.WriteFile(path, []byte(desc), 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := Add(filepath.Join(t.TempDir(), "watchers"), "cal", path, false)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func invocation(t *testing.T, w Watcher) programs.Invocation {
	return programs.Invocation{Executable: w.Program.Executable, Dir: t.TempDir(), Env: programs.Environment(t.TempDir(), nil)}
}

func TestParse(t *testing.T) {
	for _, bad := range []string{
		"executable: ./w\n",                             // no mode
		"executable: ./w\nmode: often\n",                // unknown mode
		"executable: ./w\nmode: poll\ninterval: 1s\n",   // too often
		"executable: ./w\nmode: stream\ninterval: 1m\n", // interval is a poll's
		"executable: ./w\nmode: poll\nwrites: true\n",   // a watcher never writes
		"mode: poll\n", // no executable
		"executable: ./w\nmode: poll\nname: other\n",          // another name
		"executable: ./w\nmode: poll\nsecrets: [{name: A}]\n", // a bad secret name
	} {
		if _, err := Parse("cal", bad); err == nil {
			t.Errorf("parsed %q", bad)
		}
	}
	w, err := Parse("cal", "executable: ./w\nmode: poll\ninterval: 10s\nsecrets: [{name: cal-token}]\n")
	if err != nil || w.Interval != 10*time.Second || w.Timeout != DefaultTimeout || w.Program.Secrets[0].Env != "CAL_TOKEN" || w.Program.Writes {
		t.Fatalf("%+v %v", w, err)
	}
}

func TestParseLine(t *testing.T) {
	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	ev, c, err := ParseLine([]byte(`{"id":"1","type":"meeting.started","at":"2026-11-20T09:30:00+01:00","data":{"link":"x"}}`), now)
	if err != nil || c != nil || ev.ID != "1" || ev.At.UTC().Hour() != 8 || ev.Data["link"] != "x" {
		t.Fatalf("%+v %v %v", ev, c, err)
	}
	if ev, _, _ := ParseLine([]byte(`{"id":"2","type":"x"}`), now); !ev.At.Equal(now) {
		t.Fatalf("an event without at isn't now: %v", ev.At)
	}
	if _, c, err := ParseLine([]byte(`{"cursor":"abc"}`), now); err != nil || *c != "abc" {
		t.Fatalf("cursor: %v %v", c, err)
	}
	if ev, c, err := ParseLine([]byte("   "), now); ev != nil || c != nil || err != nil {
		t.Fatal("a blank line is something")
	}
	for _, bad := range []string{`{"type":"x"}`, `{"id":"1"}`, `{"id":"1","type":"has space"}`, `{"id":"1","type":"x","at":"today"}`,
		`{"id":"1","type":"x","extra":1}`, `{"cursor":"c","id":"1"}`, `not json`, "{\"id\":\"a\\nb\",\"type\":\"x\"}"} {
		if _, _, err := ParseLine([]byte(bad), now); err == nil {
			t.Errorf("read %s", bad)
		}
	}
}

// A poll gets its cursor, and returns its events and its new cursor; one
// that fails returns nothing and leaves the cursor where it was.
func TestPoll(t *testing.T) {
	w := script(t, "executable: ./watch\nmode: poll\n", `
echo "cursor was [$PA_CURSOR] for $PA_WATCHER" >&2
if [ "$PA_CURSOR" = "bad" ]; then echo '{"id":"x","type":"t"}'; exit 3; fi
echo '{"id":"e1","type":"meeting.started","data":{"title":"Standup"}}'
echo
echo '{"id":"e2","type":"meeting.started"}'
echo '{"cursor":"after-e2"}'
`)
	var log strings.Builder
	inv := invocation(t, w)
	inv.Log = &log
	evs, cursor, err := Poll(context.Background(), w, inv, "start")
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 || evs[0].ID != "e1" || evs[0].Watcher != "cal" || evs[0].Data["title"] != "Standup" || cursor != "after-e2" {
		t.Fatalf("%+v %q", evs, cursor)
	}
	if !strings.Contains(log.String(), "cursor was [start] for cal") {
		t.Fatalf("its stderr: %q", log.String())
	}
	evs, cursor, err = Poll(context.Background(), w, invocation(t, w), "bad")
	if err == nil || len(evs) != 0 || cursor != "bad" {
		t.Fatalf("a failed poll: %v %+v %q", err, evs, cursor)
	}

	slow := script(t, "executable: ./watch\nmode: poll\ntimeout: 200ms\n", "sleep 5\n")
	if _, _, err := Poll(context.Background(), slow, invocation(t, slow), ""); err == nil || !strings.Contains(err.Error(), "took longer") {
		t.Fatalf("a slow poll: %v", err)
	}
	junk := script(t, "executable: ./watch\nmode: poll\n", "echo hello\n")
	if _, _, err := Poll(context.Background(), junk, invocation(t, junk), ""); err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("a poll printing junk: %v", err)
	}
}

// A stream's events and cursors come as it prints them; a bad line is
// skipped; it returns when it ends, or when ctx does.
func TestStream(t *testing.T) {
	w := script(t, "executable: ./watch\nmode: stream\n", `
echo '{"id":"s1","type":"email.received"}'
echo 'junk'
echo '{"cursor":"c1"}'
printf '{"id":"s2","type":"email.received"}'
`)
	var mu sync.Mutex
	var got []string
	var cursor string
	var log strings.Builder
	inv := invocation(t, w)
	inv.Log = &log
	err := Stream(context.Background(), w, inv, "", func(e Event) {
		mu.Lock()
		got = append(got, e.ID+"@"+e.Watcher)
		mu.Unlock()
	}, func(c string) { cursor = c })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "s1@cal s2@cal" || cursor != "c1" || !strings.Contains(log.String(), "skipped a line") {
		t.Fatalf("%v %q %q", got, cursor, log.String())
	}

	long := script(t, "executable: ./watch\nmode: stream\n", "echo '{\"id\":\"a\",\"type\":\"t\"}'\nexec sleep 30\n")
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Stream(ctx, long, invocation(t, long), "", func(Event) { close(first) }, func(string) {})
	}()
	select {
	case <-first:
	case <-time.After(5 * time.Second):
		t.Fatal("no event from a running stream")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a stream stopped by ctx: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a stream didn't stop with ctx")
	}
}

// What's seen and the cursor survive a restart (the state file); an event
// seen before isn't new.
func TestStateAcrossRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watcher-state.json")
	now := time.Now()
	st, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	ws := st.Of("cal")
	ws.Cursor = "c9"
	if fresh := ws.New([]Event{{ID: "1"}, {ID: "2"}, {ID: "1"}}, now); len(fresh) != 2 {
		t.Fatalf("fresh %+v", fresh)
	}
	if err := st.Save(path); err != nil {
		t.Fatal(err)
	}
	again, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	ws = again.Of("cal")
	if ws.Cursor != "c9" {
		t.Fatalf("cursor %q", ws.Cursor)
	}
	if fresh := ws.New([]Event{{ID: "2"}, {ID: "3"}}, now); len(fresh) != 1 || fresh[0].ID != "3" {
		t.Fatalf("after a restart: %+v", fresh)
	}
	// Seen IDs are kept for SeenFor.
	if fresh := ws.New(nil, now.Add(SeenFor+time.Hour)); len(fresh) != 0 || len(ws.Seen) != 0 {
		t.Fatalf("old seen IDs kept: %v", ws.Seen)
	}
}
