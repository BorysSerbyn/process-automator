package update

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Decide installs the newer release only at a quiet moment, and says why
// not otherwise; it checks every CheckEvery whether or not updates are on.
func TestDecide(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.Local)
	ago := func(d time.Duration) *time.Time { t := now.Add(-d); return &t }
	in := func(d time.Duration) *time.Time { t := now.Add(d); return &t }
	quiet := AutoState{CheckedAt: ago(time.Minute), Latest: "v2.0.0", Downloadable: true, SeenAt: ago(2 * time.Hour)}
	facts := func() Facts {
		return Facts{Now: now, Enabled: true, Current: "1.0.0", AwakeSince: now.Add(-time.Hour),
			FullyAwake: func() bool { return true }, When: func(t, _ time.Time) string { return t.Format("15:04") }}
	}
	for _, tc := range []struct {
		name    string
		st      func(*AutoState)
		f       func(*Facts)
		install bool
		waiting string
	}{
		{name: "quiet", install: true},
		{name: "off", f: func(f *Facts) { f.Enabled = false }},
		{name: "a dev build", f: func(f *Facts) { f.Blocked = "a development build" }},
		{name: "installing", f: func(f *Facts) { f.Installing = true }},
		{name: "up to date", f: func(f *Facts) { f.Current = "2.0.0" }},
		{name: "newer than the release", f: func(f *Facts) { f.Current = "2.0.1-dev+abc" }},
		{name: "no download", st: func(s *AutoState) { s.Downloadable = false }, waiting: "has no download"},
		{name: "just published", st: func(s *AutoState) { s.SeenAt = ago(10 * time.Minute) }, waiting: "after 12:50"},
		{name: "a run", f: func(f *Facts) { f.Busy = "standup is running" }, waiting: "once nothing is running (standup is running)"},
		{name: "a run due", f: func(f *Facts) { f.Next, f.NextName = in(time.Minute), "standup" }, waiting: "after standup's run at 12:01"},
		{name: "a run later", f: func(f *Facts) { f.Next, f.NextName = in(time.Hour), "standup" }, install: true},
		{name: "in use", st: func(s *AutoState) { s.PostponedUntil = in(time.Minute) }, waiting: "window is closed"},
		{name: "was in use", st: func(s *AutoState) { s.PostponedUntil = ago(time.Second) }, install: true},
		{name: "just woke", f: func(f *Facts) { f.AwakeSince = now.Add(-time.Minute) }, waiting: "awake"},
		{name: "a maintenance wake", f: func(f *Facts) { f.FullyAwake = func() bool { return false } }, waiting: "awake"},
		{name: "failed", st: func(s *AutoState) { s.Tried, s.TriedAt = "v2.0.0", ago(time.Hour) }, waiting: "didn't finish"},
		{name: "failed long ago", st: func(s *AutoState) { s.Tried, s.TriedAt = "v2.0.0", ago(25*time.Hour) }, install: true},
		{name: "an older one failed", st: func(s *AutoState) { s.Tried, s.TriedAt = "v1.5.0", ago(time.Hour) }, install: true},
	} {
		st, f := quiet, facts()
		if tc.st != nil {
			tc.st(&st)
		}
		if tc.f != nil {
			tc.f(&f)
		}
		plan := Decide(st, f)
		if (plan.Install != "") != tc.install || (tc.install && plan.Install != "v2.0.0") {
			t.Errorf("%s: install %q", tc.name, plan.Install)
		}
		if tc.waiting == "" && plan.Waiting != "" || !strings.Contains(plan.Waiting, tc.waiting) {
			t.Errorf("%s: waiting %q, want %q", tc.name, plan.Waiting, tc.waiting)
		}
		if plan.Check {
			t.Errorf("%s: checked a minute after the last check", tc.name)
		}
	}
	for _, tc := range []struct {
		checked *time.Time
		want    bool
	}{{nil, true}, {ago(59 * time.Minute), false}, {ago(time.Hour), true}, {in(time.Hour), true}} {
		f := facts()
		f.Enabled = false
		if got := Decide(AutoState{CheckedAt: tc.checked}, f).Check; got != tc.want {
			t.Errorf("checked %v: check = %v", tc.checked, got)
		}
	}
}

// A check records what it found: when a release was first seen, kept while
// it stays the latest; a failed check keeps the last answer.
func TestRecordCheck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update-auto.json")
	t0 := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	load := func() AutoState {
		t.Helper()
		st, err := LoadAuto(path)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	if st := load(); st.CheckedAt != nil {
		t.Fatalf("empty: %+v", st)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(RecordCheck(path, "v2.0.0", true, nil, t0))
	must(RecordCheck(path, "v2.0.0", true, nil, t0.Add(time.Hour)))
	if st := load(); st.Latest != "v2.0.0" || !st.Downloadable || !st.SeenAt.Equal(t0) || !st.CheckedAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("seen again: %+v", st)
	}
	must(RecordCheck(path, "", false, errors.New("no network"), t0.Add(2*time.Hour)))
	if st := load(); st.Latest != "v2.0.0" || st.CheckError != "no network" || !st.CheckedAt.Equal(t0.Add(2*time.Hour)) {
		t.Fatalf("failed: %+v", st)
	}
	must(RecordCheck(path, "v2.1.0", false, nil, t0.Add(3*time.Hour)))
	if st := load(); st.Latest != "v2.1.0" || st.Downloadable || !st.SeenAt.Equal(t0.Add(3*time.Hour)) || st.CheckError != "" {
		t.Fatalf("a newer one: %+v", st)
	}
	until, err := Postpone(path, t0)
	must(err)
	must(RecordTry(path, "v2.1.0", t0))
	if st := load(); !st.PostponedUntil.Equal(until) || !until.Equal(t0.Add(PostponeFor)) || st.Tried != "v2.1.0" {
		t.Fatalf("postponed, tried: %+v", st)
	}
	// An earlier postpone never shortens a later one.
	_, err = Postpone(path, t0.Add(-time.Hour))
	must(err)
	if st := load(); !st.PostponedUntil.Equal(until) {
		t.Fatalf("shortened: %+v", st)
	}
}
