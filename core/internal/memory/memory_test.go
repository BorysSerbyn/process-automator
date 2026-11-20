package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStore(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	g, err := s.Add(Global, "  Weeks start on Monday ")
	if err != nil || g.Text != "Weeks start on Monday" {
		t.Fatal(g, err)
	}
	if _, err := s.Add(Global, "Weeks start on Monday"); err == nil {
		t.Fatal("duplicate accepted")
	}
	a, _ := s.Add("scrum", "The team's thread is in #team-daily")
	_, _ = s.Add("other", "not for scrum")
	r, _ := s.Render("scrum")
	if !strings.Contains(r, "Weeks start on Monday") || !strings.Contains(r, "#team-daily") || strings.Contains(r, "not for scrum") {
		t.Fatalf("render: %s", r)
	}
	if r, _ := s.Render("nobody"); !strings.Contains(r, "Monday") {
		t.Fatalf("a routine without its own memories gets the global ones: %q", r)
	}
	// A note I wrote by hand survives edits.
	path := filepath.Join(s.Dir, "scrum.md")
	b, _ := os.ReadFile(path)
	_ = os.WriteFile(path, append([]byte("My own note.\n"), b...), 0o600)
	e, err := s.Edit(a.ID, "The team's thread is in #team-standup")
	if err != nil || e.ID == a.ID {
		t.Fatal(e, err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "My own note.") || !strings.Contains(string(b), "standup") {
		t.Fatalf("file: %s", b)
	}
	if _, err := s.Remove(g.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Remove(g.ID); err == nil {
		t.Fatal("removed twice")
	}
	if err := s.Replace("scrum", []string{"one", "one", " two "}); err != nil {
		t.Fatal(err)
	}
	if ms, _ := s.List("scrum"); len(ms) != 2 || ms[1].Text != "two" {
		t.Fatalf("replace: %+v", ms)
	}
	if err := s.Forget("scrum"); err != nil {
		t.Fatal(err)
	}
	if ms, _ := s.List("scrum"); len(ms) != 0 {
		t.Fatal("forgot nothing")
	}
}
