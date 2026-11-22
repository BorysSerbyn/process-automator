package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

// now runs work and what it returns at once: the tests' Async.
func now(work func() func()) { work()() }

// fakeCLI is a stand-in for the app's CLI: a script that logs each command
// and answers it from replies (by its arguments, joined by spaces): a
// testdata file's name ("@setup-local.json"), "!message" for the core's
// error, or the text itself. Anything else answers {}.
type fakeCLI struct {
	cli.CLI
	log string
}

func newFakeCLI(t *testing.T, replies map[string]string) *fakeCLI {
	t.Helper()
	dir := t.TempDir()
	f := &fakeCLI{log: filepath.Join(dir, "log")}
	var script strings.Builder
	fmt.Fprintf(&script, "#!/bin/sh\necho \"$*\" >> '%s'\ncase \"$*\" in\n", f.log)
	i := 0
	for args, reply := range replies {
		i++
		out := filepath.Join(dir, fmt.Sprintf("reply%d", i))
		code := 0
		switch {
		case strings.HasPrefix(reply, "@"):
			b, err := os.ReadFile(filepath.Join("testdata", reply[1:]))
			if err != nil {
				t.Fatal(err)
			}
			reply = string(b)
		case strings.HasPrefix(reply, "!"):
			reply, code = fmt.Sprintf(`{"error": %q}`, reply[1:]), 1
		}
		if err := os.WriteFile(out, []byte(reply), 0o600); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&script, "  '%s') cat '%s'; exit %d ;;\n", args, out, code)
	}
	script.WriteString("  *) echo '{}' ;;\nesac\n")
	path := filepath.Join(dir, "acme-auto")
	if err := os.WriteFile(path, []byte(script.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	f.CLI = cli.CLI{Path: path, Command: "acme-auto"}
	return f
}

// ran is every command run so far.
func (f *fakeCLI) ran() []string {
	b, _ := os.ReadFile(f.log)
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// count is how many times a command ran.
func (f *fakeCLI) count(args string) int {
	n := 0
	for _, r := range f.ran() {
		if r == args {
			n++
		}
	}
	return n
}

func (f *fakeCLI) mustHaveRun(t *testing.T, args ...string) {
	t.Helper()
	for _, a := range args {
		if !slices.Contains(f.ran(), a) {
			t.Errorf("didn't run %q; ran:\n%s", a, strings.Join(f.ran(), "\n"))
		}
	}
}

// window is a test window showing content.
func window(t *testing.T) fyne.Window {
	t.Helper()
	a := test.NewTempApp(t)
	w := a.NewWindow("test")
	w.Resize(fyne.NewSize(640, 680))
	return w
}

// objects are everything content shows, renderers included.
func objects(content fyne.CanvasObject) []fyne.CanvasObject { return test.LaidOutObjects(content) }

// texts is all the text content shows, one string per label or rich text.
func texts(content fyne.CanvasObject) []string {
	var out []string
	for _, o := range objects(content) {
		switch v := o.(type) {
		case *widget.Label:
			out = append(out, v.Text)
		case *widget.RichText:
			out = append(out, v.String())
		case *canvas.Text:
			out = append(out, v.Text)
		}
	}
	return out
}

func hasText(content fyne.CanvasObject, s string) bool {
	for _, t := range texts(content) {
		if strings.Contains(t, s) {
			return true
		}
	}
	return false
}

func mustShow(t *testing.T, content fyne.CanvasObject, want ...string) {
	t.Helper()
	for _, s := range want {
		if !hasText(content, s) {
			t.Errorf("doesn't show %q; shows:\n%s", s, strings.Join(texts(content), "\n"))
		}
	}
}

func mustNotShow(t *testing.T, content fyne.CanvasObject, s string) {
	t.Helper()
	if hasText(content, s) {
		t.Errorf("shows %q", s)
	}
}

// all are content's objects of type T.
func all[T fyne.CanvasObject](content fyne.CanvasObject) []T {
	var out []T
	for _, o := range objects(content) {
		if v, ok := o.(T); ok {
			out = append(out, v)
		}
	}
	return out
}

// buttons are content's buttons labelled text.
func buttons(content fyne.CanvasObject, text string) []*widget.Button {
	var out []*widget.Button
	for _, b := range all[*widget.Button](content) {
		if b.Text == text {
			out = append(out, b)
		}
	}
	return out
}

func button(t *testing.T, content fyne.CanvasObject, text string) *widget.Button {
	t.Helper()
	b := buttons(content, text)
	if len(b) == 0 {
		t.Fatalf("no %q button", text)
	}
	return b[0]
}

// rowOf is the disclosure row whose content shows text.
func rowOf(t *testing.T, content fyne.CanvasObject, text string) *disclosure {
	t.Helper()
	for _, d := range all[*disclosure](content) {
		if hasText(d.content, text) {
			return d
		}
	}
	t.Fatalf("no row showing %q", text)
	return nil
}
