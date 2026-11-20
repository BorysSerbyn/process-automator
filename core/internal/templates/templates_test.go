package templates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BorysSerbyn/process-automator/core/internal/routines"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) string {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "personal", "digest.prompt"), `---
description: Finds open requests
automator.parameters:
  - {name: lookback, type: duration, default: 72h, description: How far back}
  - {name: channel, type: string, description: Chat channel ID}
---
Look back {{lookback}} in {{channel}}.`)
	other := "linux"
	if routines.Platform() == "linux" {
		other = "macos"
	}
	write(t, filepath.Join(dir, "projects", "acme", "elsewhere.prompt"), "---\nautomator.platforms: ["+other+"]\n---\nhi")
	write(t, filepath.Join(dir, "general.prompt"), "---\ndescription: Top level\n---\nhi")
	return dir
}

func TestList(t *testing.T) {
	list, err := List(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, tpl := range list {
		ids = append(ids, tpl.ID)
	}
	if got := strings.Join(ids, ","); got != "general,personal/digest,projects/acme/elsewhere" {
		t.Fatalf("ids = %s", got)
	}
	digest := list[1]
	if strings.Join(digest.Tags, "/") != "personal" || digest.Description != "Finds open requests" || len(digest.Params) != 2 ||
		!digest.Params[1].Required || digest.Params[0].Required {
		t.Fatalf("digest: %+v", digest)
	}
	if list[2].RunsHere() || !list[0].RunsHere() {
		t.Fatal("platform filtering wrong")
	}
	if len(list[0].Tags) != 0 {
		t.Fatalf("top-level template has no tags: %v", list[0].Tags)
	}
	if list[2].Project != "acme" || list[0].Project != "" || list[1].Project != "" {
		t.Fatalf("projects: %q %q %q", list[0].Project, list[1].Project, list[2].Project)
	}
}

func TestFindAndCopy(t *testing.T) {
	dir := fixture(t)
	if _, err := Find(dir, "personal/nope"); err == nil || !strings.Contains(err.Error(), "personal/digest") {
		t.Fatalf("unknown template should list what exists: %v", err)
	}
	tpl, err := Find(dir, "personal/digest")
	if err != nil {
		t.Fatal(err)
	}
	rd := filepath.Join(t.TempDir(), "routines")
	path, err := Copy(tpl, rd, "digest")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "{{lookback}}") {
		t.Fatal("installed routine must keep the parameters")
	}
	if _, err := Copy(tpl, rd, "digest"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("must never overwrite a routine: %v", err)
	}
}

func TestBrokenTemplateIsAnError(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "x", "bad.prompt"), "---\nautomator.nope: 1\n---\nhi")
	if _, err := List(dir); err == nil {
		t.Fatal("a template that doesn't load must be an error")
	}
}

func TestNoTemplates(t *testing.T) {
	for name, dir := range map[string]string{"empty": t.TempDir(), "missing": filepath.Join(t.TempDir(), "missing")} {
		list, err := List(dir)
		if err != nil || len(list) != 0 {
			t.Fatalf("%s templates folder: %v %v", name, list, err)
		}
		if _, err := Find(dir, "personal/digest"); err == nil || !strings.Contains(err.Error(), "no templates") {
			t.Fatalf("%s templates folder: find must say there are none: %v", name, err)
		}
	}
	// A folder with only a README (as the repo's templates/ has) lists none too.
	dir := t.TempDir()
	write(t, filepath.Join(dir, "README.md"), "# Templates")
	if list, err := List(dir); err != nil || len(list) != 0 {
		t.Fatalf("README only: %v %v", list, err)
	}
}

func TestProjects(t *testing.T) {
	if p := projectOf([]string{"projects", "acme"}); p != "acme" {
		t.Errorf("project %q", p)
	}
	for _, tags := range [][]string{{"projects"}, {"personal"}, nil} {
		if p := projectOf(tags); p != "" {
			t.Errorf("%v: project %q", tags, p)
		}
	}
	list := []Template{{Project: "globex"}, {}, {Project: "acme"}, {Project: "globex"}}
	if got := Projects(list); len(got) != 2 || got[0] != "acme" || got[1] != "globex" {
		t.Errorf("projects %v", got)
	}
}
