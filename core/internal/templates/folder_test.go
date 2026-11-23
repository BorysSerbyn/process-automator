//go:build unix

package templates

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeExe(t *testing.T, path, body string) {
	t.Helper()
	write(t, path, body)
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

// folders: a watcher built from a shared source folder, with a parameter
// in its args; a program hook on it; a script program.
func folders(t *testing.T) string {
	dir := t.TempDir()
	writeExe(t, filepath.Join(dir, "github", "src", "gh.sh"), "#!/bin/sh\necho built\n")
	write(t, filepath.Join(dir, "github", "gh-reviews", ManifestFile), `description: Pull requests waiting for me
params:
  - {name: login, type: string, description: My GitHub login}
build:
  command: [cp, gh.sh, "{{output}}"]
  dir: ../src
`)
	write(t, filepath.Join(dir, "github", "gh-reviews", "watcher.yaml"), `executable: ./gh
mode: poll
interval: 5m
args: [author, "{{login}}"]
events:
  review.requested: A review is requested of me
`)
	write(t, filepath.Join(dir, "github", "pr-review-requested", ManifestFile), "description: Notifies me of a review request\n")
	write(t, filepath.Join(dir, "github", "pr-review-requested", "hook.yaml"), `executable: ./gh
writes: false
args: [notify]
on: {watcher: gh-reviews, type: review.requested}
`)
	writeExe(t, filepath.Join(dir, "github", "pr-review-requested", "gh"), "#!/bin/sh\ntrue\n")
	write(t, filepath.Join(dir, "personal", "cleaner", ManifestFile), "platforms: [macos, linux]\n")
	write(t, filepath.Join(dir, "personal", "cleaner", "program.yaml"), "description: Cleans\nexecutable: ./clean\nwrites: true\n")
	writeExe(t, filepath.Join(dir, "personal", "cleaner", "clean"), "#!/bin/sh\ntrue\n")
	write(t, filepath.Join(dir, "personal", "digest.prompt"), "---\ndescription: d\n---\n{{> memories}} {{today}}")
	return dir
}

func TestFolderTemplates(t *testing.T) {
	dir := folders(t)
	list, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, x := range list {
		got = append(got, x.ID+":"+x.Kind)
	}
	if s := strings.Join(got, " "); s != "github/gh-reviews:watcher github/pr-review-requested:hook personal/cleaner:program personal/digest:routine" {
		t.Fatalf("listed %s", s)
	}
	w, hook, prog := list[0], list[1], list[2]
	if w.Description != "Pull requests waiting for me" || len(w.Params) != 1 || !w.Params[0].Required || w.Build == nil ||
		hook.Watcher != "gh-reviews" || prog.Description != "Cleans" {
		t.Fatalf("%+v\n%+v\n%+v", w, hook, prog)
	}
	if got, err := WatcherOf(list, hook); err != nil || got.ID != w.ID {
		t.Fatal(got.ID, err)
	}
	if err := Check(dir); err != nil {
		t.Fatal(err)
	}

	// Staging fills in parameters and builds (or copies) the executable.
	tmp := t.TempDir()
	desc, err := w.Stage(context.Background(), map[string]string{"login": "rick"}, tmp, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	text, _ := os.ReadFile(desc)
	built, err := os.ReadFile(filepath.Join(filepath.Dir(desc), "gh"))
	if !strings.Contains(string(text), `args: [author, "rick"]`) || err != nil || string(built) != "#!/bin/sh\necho built\n" {
		t.Fatalf("staged %s, built %q %v", text, built, err)
	}
	if _, err := prog.Stage(context.Background(), nil, t.TempDir(), io.Discard); err != nil {
		t.Fatal(err)
	}

	// A missing toolchain is named.
	w.Build = &Build{Command: []string{"no-such-compiler", "-o", OutputPlaceholder}}
	if _, err := w.Stage(context.Background(), nil, t.TempDir(), io.Discard); err == nil || !strings.Contains(err.Error(), "no-such-compiler, which isn't installed") {
		t.Fatalf("missing toolchain: %v", err)
	}
	// A build that fails says what it said.
	w.Build = &Build{Command: []string{"sh", "-c", "echo broken >&2; exit 1", OutputPlaceholder}}
	if _, err := w.Stage(context.Background(), nil, t.TempDir(), io.Discard); err == nil || !strings.Contains(err.Error(), "broken") {
		t.Fatalf("failed build: %v", err)
	}
}

// Check finds what's wrong with a template folder.
func TestCheckFolders(t *testing.T) {
	for name, tc := range map[string]struct {
		edit func(dir string)
		want string
	}{
		"hook on a watcher no template is": {func(dir string) {
			write(t, filepath.Join(dir, "github", "pr-review-requested", "hook.yaml"), "executable: ./gh\nwrites: false\non: {watcher: nope, type: x}\n")
		}, "no template is that watcher"},
		"hook on a type its watcher lacks": {func(dir string) {
			write(t, filepath.Join(dir, "github", "pr-review-requested", "hook.yaml"), "executable: ./gh\nwrites: false\non: {watcher: gh-reviews, type: pr.opened}\n")
		}, "doesn't declare"},
		"undeclared placeholder": {func(dir string) {
			write(t, filepath.Join(dir, "personal", "cleaner", "program.yaml"), "executable: ./clean\nwrites: true\nargs: [\"{{who}}\"]\n")
		}, "{{who}}"},
		"build without output": {func(dir string) {
			write(t, filepath.Join(dir, "github", "gh-reviews", ManifestFile), "params: [{name: login, type: string}]\nbuild: {command: [cp, gh.sh, x], dir: ../src}\n")
		}, "doesn't say where"},
		"build outside": {func(dir string) {
			write(t, filepath.Join(dir, "github", "gh-reviews", ManifestFile), "params: [{name: login, type: string}]\nbuild: {command: [cp, gh.sh, \"{{output}}\"], dir: ../../..}\n")
		}, "outside the templates folder"},
		"executable missing": {func(dir string) {
			_ = os.Remove(filepath.Join(dir, "personal", "cleaner", "clean"))
		}, "isn't there"},
		"two description files": {func(dir string) {
			write(t, filepath.Join(dir, "personal", "cleaner", "watcher.yaml"), "executable: ./clean\nmode: poll\n")
		}, "a template is one"},
		"unknown manifest key": {func(dir string) {
			write(t, filepath.Join(dir, "personal", "cleaner", ManifestFile), "platform: [macos]\n")
		}, "platform"},
	} {
		dir := folders(t)
		tc.edit(dir)
		if err := Check(dir); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
