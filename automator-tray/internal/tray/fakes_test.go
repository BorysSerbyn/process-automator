package tray

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/app"
	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

// fakeCLI is a stand-in for the app's CLI: a shell script that logs each
// command it's given and answers from files in its folder (answers/<the
// command, spaces as _>), as the core would.
type fakeCLI struct {
	t   *testing.T
	dir string
	cli cli.CLI
}

func newFakeCLI(t *testing.T) *fakeCLI {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "acme-auto")
	script := `#!/bin/sh
dir=$(dirname "$0")
echo "$*" >> "$dir/log"
answer="$dir/answers/$(echo "$*" | tr ' ' _)"
if [ -f "$answer.fail" ]; then cat "$answer.fail"; exit 1; fi
[ -f "$answer" ] && cat "$answer"
exit 0
`
	if err := os.MkdirAll(filepath.Join(dir, "answers"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &fakeCLI{t: t, dir: dir, cli: cli.CLI{Path: path, Command: "acme-auto"}}
}

// answer sets what a command prints.
func (f *fakeCLI) answer(command, output string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, "answers", strings.ReplaceAll(command, " ", "_")), []byte(output), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// fail makes a command fail, printing the core's error object.
func (f *fakeCLI) fail(command, message string) {
	f.answer(command+".fail", `{"error": "`+message+`"}`)
}

// ran are the commands run so far.
func (f *fakeCLI) ran() []string {
	b, _ := os.ReadFile(filepath.Join(f.dir, "log"))
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

func (f *fakeCLI) hasRun(command string) bool {
	for _, c := range f.ran() {
		if c == command {
			return true
		}
	}
	return false
}

// fakeNotifications is the desktop's notifications service, recording notes.
type fakeNotifications struct {
	mu     sync.Mutex
	notes  map[uint32]Note
	next   uint32
	refuse map[string]bool // summaries it fails to show
	canAsk bool
}

func newFakeNotifications() *fakeNotifications {
	return &fakeNotifications{notes: map[uint32]Note{}, refuse: map[string]bool{}, canAsk: true}
}

func (f *fakeNotifications) Notify(n Note) (uint32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refuse[n.Summary] {
		return 0, errors.New("refused")
	}
	f.next++
	f.notes[f.next] = n
	return f.next, nil
}

func (f *fakeNotifications) CanAsk() bool { return f.canAsk }

// find is the ID of the note with this summary (0: none).
func (f *fakeNotifications) find(summary string) uint32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, n := range f.notes {
		if n.Summary == summary {
			return id
		}
	}
	return 0
}

func (f *fakeNotifications) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.notes)
}

func (f *fakeNotifications) note(id uint32) Note {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.notes[id]
}

// windows records the windows opened.
type windows struct {
	mu     sync.Mutex
	opened []Window
	err    error
}

func (w *windows) open(_ string, win Window) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.opened = append(w.opened, win)
	return w.err
}

func (w *windows) has(win Window) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, o := range w.opened {
		if o == win {
			return true
		}
	}
	return false
}

// fakeView records what the tray shows.
type fakeView struct {
	mu    sync.Mutex
	icons []Frame
	menus [][]MenuItem
}

func (v *fakeView) SetIcon(f Frame) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.icons = append(v.icons, f)
}

func (v *fakeView) SetMenu(m []MenuItem) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.menus = append(v.menus, m)
}

func (v *fakeView) last() (Frame, []MenuItem, int, int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	var f Frame
	var m []MenuItem
	if len(v.icons) > 0 {
		f = v.icons[len(v.icons)-1]
	}
	if len(v.menus) > 0 {
		m = v.menus[len(v.menus)-1]
	}
	return f, m, len(v.icons), len(v.menus)
}

// eventually waits for cond, failing after a few seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// newTray is a tray for the fake CLI, with fake desktop services.
func newTray(t *testing.T, f *fakeCLI) (*Tray, *fakeView, *fakeNotifications, *windows) {
	t.Helper()
	v, n, w := &fakeView{}, newFakeNotifications(), &windows{}
	info := app.Info{Name: "Acme Automator", Command: "acme-auto", CLI: f.cli.Path, Version: "2.1.0"}
	tr := New(info, v, n, w.open, func() {})
	return tr, v, n, w
}
