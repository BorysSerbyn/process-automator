package trust

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// A trust file written before programs (a surface per routine) reads as it was.
func TestReadsSurfacesAsBefore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trust.json")
	body := `{"schemaVersion":1,"routines":{"standup":{"surface":{"servers":["chat"],"tools":["send"]},"trustedAt":"2026-11-01T09:00:00Z"}}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	f, ok, err := Store{Path: path}.Load()
	if err != nil || !ok {
		t.Fatal(err)
	}
	rec := f.Routines["standup"]
	cur := Approval{Surface: &Surface{Servers: []string{"chat"}, Tools: []string{"send"}}}
	if rec.Program != nil || !cur.Equal(rec.Approval) {
		t.Fatalf("%+v", rec)
	}
}

// A program's trust is its checksums: another executable or description
// file is a change, said in words.
func TestProgramChanges(t *testing.T) {
	old := Approval{Program: &Program{Executable: "aaa", Description: "d1", Secrets: []string{"key"}}}
	same := Approval{Program: &Program{Executable: "aaa", Description: "d1", Secrets: []string{"key"}}}
	if !same.Equal(old) || len(same.Changes(old)) != 0 {
		t.Fatal("the same program changed")
	}
	for want, cur := range map[string]Approval{
		"has another executable":       {Program: &Program{Executable: "bbb", Description: "d1", Secrets: []string{"key"}}},
		"changes its description file": {Program: &Program{Executable: "aaa", Description: "d2", Secrets: []string{"key"}}},
		"now writes":                   {Program: &Program{Executable: "aaa", Description: "d2", Writes: true, Secrets: []string{"key"}}},
		"adds secret other":            {Program: &Program{Executable: "aaa", Description: "d2", Secrets: []string{"key", "other"}}},
		"is an LLM routine now":        {Surface: &Surface{}},
	} {
		if cur.Equal(old) || !strings.Contains(strings.Join(cur.Changes(old), "; "), want) {
			t.Errorf("%s: %v", want, cur.Changes(old))
		}
	}
	path := filepath.Join(t.TempDir(), "trust.json")
	st := Store{Path: path}
	if err := st.Trust(map[string]Approval{"backup": old}, time.Now()); err != nil {
		t.Fatal(err)
	}
	f, _, _ := st.Load()
	if rec := f.Routines["backup"]; rec.Surface != nil || !rec.Equal(old) {
		t.Fatalf("%+v", rec)
	}
}

// Trust covers permissions: one needed now, or no longer, is a change, said
// in words; a program's changed description file isn't said twice.
func TestPermissionChanges(t *testing.T) {
	old := Approval{Program: &Program{Executable: "aaa", Description: "d1"}, Permissions: []string{"notifications"}}
	cur := Approval{Program: &Program{Executable: "aaa", Description: "d2"}, Permissions: []string{"input-monitoring", "notifications"}}
	if cur.Equal(old) {
		t.Fatal("a permission added changed nothing")
	}
	if got := cur.Changes(old); len(got) != 1 || got[0] != "needs the input-monitoring permission now" {
		t.Fatalf("%v", got)
	}
	if got := old.Changes(cur); !slices.Contains(got, "no longer needs the input-monitoring permission") {
		t.Fatalf("%v", got)
	}
	same := Approval{Program: &Program{Executable: "aaa", Description: "d1"}, Permissions: []string{"notifications", "notifications"}}
	if !same.Equal(old) {
		t.Fatal("the same permissions changed")
	}
}

// A record from before permissions were declared covers whatever they are
// now, until they're recorded; from then on, one added is a change.
func TestRecordsFromBeforePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trust.json")
	body := `{"schemaVersion":1,"routines":{"digest":{"surface":{"servers":["notify"],"tools":["notify_me"]},"trustedAt":"2026-11-01T09:00:00Z"}}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	st := Store{Path: path}
	surface := &Surface{Servers: []string{"notify"}, Tools: []string{"notify_me"}}
	cur := Approval{Surface: surface, Permissions: []string{"notifications"}}
	f, _, _ := st.Load()
	if rec := f.Routines["digest"]; !rec.Unrecorded() || !rec.Approves(cur) || len(rec.ChangesTo(cur)) != 0 {
		t.Fatalf("an old record: %+v", rec)
	}
	// Another record written meanwhile leaves it as it was.
	if err := st.Trust(map[string]Approval{"other": {Surface: &Surface{}}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if f, _, _ = st.Load(); !f.Routines["digest"].Unrecorded() || f.Routines["other"].Unrecorded() {
		t.Fatalf("%+v", f.Routines)
	}
	if err := st.RecordPermissions(map[string][]string{"digest": {"notifications"}, "other": {"camera"}}); err != nil {
		t.Fatal(err)
	}
	f, _, _ = st.Load()
	rec := f.Routines["digest"]
	if rec.Unrecorded() || !rec.Approves(cur) || len(f.Routines["other"].Permissions) != 0 {
		t.Fatalf("recorded: %+v", f.Routines)
	}
	more := Approval{Surface: surface, Permissions: []string{"input-monitoring", "notifications"}}
	if rec.Approves(more) || !slices.Contains(rec.ChangesTo(more), "needs the input-monitoring permission now") {
		t.Fatalf("a permission added after: %v", rec.ChangesTo(more))
	}
}
