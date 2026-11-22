package permissions

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Declared permissions are known ones, sorted, each once; anything else is
// refused, naming the ones there are.
func TestCheck(t *testing.T) {
	got, err := Check("program.yaml", []string{"notifications", "input-monitoring", "notifications"})
	if err != nil || !slices.Equal(got, []string{"input-monitoring", "notifications"}) {
		t.Fatalf("%v %v", got, err)
	}
	if got, err := Check("program.yaml", nil); err != nil || got != nil {
		t.Fatalf("none: %v %v", got, err)
	}
	_, err = Check("program.yaml", []string{"keyboard"})
	if err == nil || !strings.Contains(err.Error(), `unknown permission "keyboard"`) || !strings.Contains(err.Error(), "input-monitoring") {
		t.Fatalf("an unknown permission: %v", err)
	}
	if len(All) != 11 || Label("screen-recording") != "Screen Recording" || Known("keyboard") {
		t.Fatal("the set")
	}
}

// Nothing recorded reads as not checked, never as granted; what's recorded
// reads back, and recording keeps the requests taken.
func TestState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "permissions.json")
	s, err := Load(path)
	if err != nil || s != nil || s.Of("notifications") != NotChecked {
		t.Fatalf("nothing recorded: %+v %v", s, err)
	}
	now := time.Now().Round(time.Second)
	if err := Update(path, func(s *State) {
		s.Requests = map[string]Request{"notifications": {At: now, Asked: true}}
	}); err != nil {
		t.Fatal(err)
	}
	if err := Update(path, func(s *State) {
		s.CheckedAt, s.Permissions = now, map[string]Status{"notifications": Granted, "camera": Missing}
	}); err != nil {
		t.Fatal(err)
	}
	s, err = Load(path)
	if err != nil || s.Of("notifications") != Granted || s.Of("camera") != Missing || s.Of("contacts") != NotChecked ||
		!s.CheckedAt.Equal(now) || !s.Requests["notifications"].Asked {
		t.Fatalf("%+v %v", s, err)
	}
	if !Missing.Blocks() || NotChecked.Blocks() || Unknown.Blocks() || Unsupported.Blocks() || Granted.Blocks() {
		t.Fatal("only missing keeps anything from running")
	}
}
