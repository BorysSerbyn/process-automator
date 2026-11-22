package trust

import (
	"os"
	"path/filepath"
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
