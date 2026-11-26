//go:build unix

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
)

// templateLab is a templates folder and an app to install from it.
type templateLab struct {
	t    *testing.T
	tdir string
	cfg  string
}

func newTemplateLab(t *testing.T) *templateLab {
	l := &templateLab{t: t, tdir: t.TempDir(), cfg: filepath.Join(t.TempDir(), "config.yaml")}
	old := product.TemplatesDir
	product.TemplatesDir = l.tdir
	t.Cleanup(func() { product.TemplatesDir = old })
	return l
}

func (l *templateLab) put(path, body string, mode os.FileMode) {
	l.t.Helper()
	p := filepath.Join(l.tdir, path)
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		l.t.Fatal(err)
	}
}

func (l *templateLab) run(args ...string) (string, error) {
	root := NewRoot(nil)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(append(args, "--config", l.cfg))
	err := root.Execute()
	return out.String(), err
}

func (l *templateLab) must(args ...string) string {
	l.t.Helper()
	out, err := l.run(args...)
	if err != nil {
		l.t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return out
}

func (l *templateLab) outdated(all bool) map[string]engine.TemplateUpdate {
	l.t.Helper()
	args := []string{"templates", "outdated", "--json"}
	if all {
		args = append(args, "--all")
	}
	var ups []engine.TemplateUpdate
	out := l.must(args...)
	if err := json.Unmarshal([]byte(out), &ups); err != nil {
		l.t.Fatalf("outdated: %v %s", err, out)
	}
	m := map[string]engine.TemplateUpdate{}
	for _, u := range ups {
		m[u.Name] = u
	}
	return m
}

func (l *templateLab) app() *engine.App {
	l.t.Helper()
	a, err := loadTest(l.cfg, nil)
	if err != nil {
		l.t.Fatal(err)
	}
	return a
}

func digest(version, body string) string {
	return "---\nautomator.version: \"" + version + "\"\nautomator.changes: Now " + version + "\n" +
		"automator.outcome: {done: Posted., nothingToDo: []}\nautomator.schedule: \"0 8 * * 1-5\"\n" +
		"automator.parameters:\n  - {name: channel, type: string}\n---\n" + body + " {{channel}}.\n"
}

// A routine installed from a template is offered when its template's
// content changes (its version is only shown); overriding it keeps its
// settings and trust; a change I skip isn't offered again, a later one is.
func TestTemplateUpdatesRoutine(t *testing.T) {
	l := newTemplateLab(t)
	l.put("personal/digest.prompt", digest("1.0", "Post"), 0o600)
	l.must("templates", "install", "personal/digest", "--set", "channel=C1")
	routine := filepath.Join(filepath.Dir(l.cfg), "routines", "digest.prompt")
	if ups := l.outdated(false); len(ups) != 0 {
		t.Fatalf("just installed, offered: %+v", ups)
	}
	if out := l.must("templates", "list"); !strings.Contains(out, "1.0") {
		t.Fatalf("list doesn't show versions:\n%s", out)
	}
	l.must("routines", "trust", "digest", "--yes")

	// Its version alone doesn't decide: the same version with other content is offered.
	l.put("personal/digest.prompt", digest("1.0", "Post it"), 0o600)
	u, ok := l.outdated(false)["digest"]
	if !ok || u.Edited != engine.EditedNo || u.InstalledVersion != "1.0" || u.Version != "1.0" || u.Template != "personal/digest" {
		t.Fatalf("offered: %+v", u)
	}
	l.put("personal/digest.prompt", digest("2.0", "Post now"), 0o600)
	if u := l.outdated(false)["digest"]; u.Version != "2.0" || u.Changes != "Now 2.0" {
		t.Fatalf("offered: %+v", u)
	}
	// Overriding is mine, not an MCP tool's.
	if out := l.must("templates", "override", "digest"); !strings.Contains(out, "still trusted") {
		t.Fatalf("override: %s", out)
	}
	if b, _ := os.ReadFile(routine); string(b) != digest("2.0", "Post now") {
		t.Fatalf("routine is now:\n%s", b)
	}
	a := l.app()
	x, err := a.Find("digest")
	if err != nil {
		t.Fatal(err)
	}
	if why, _, _ := a.TrustProblem(x); why != "" {
		t.Fatalf("not trusted after override: %s", why)
	}
	set, _ := routines.Load(a.Layout.Routines())
	r, _ := set.Find("digest")
	if c, err := a.RoutineConfig(r); err != nil || c.Params["channel"] != "C1" {
		t.Fatalf("settings: %+v %v", c, err)
	}
	if ups := l.outdated(false); len(ups) != 0 {
		t.Fatalf("overridden, still offered: %+v", ups)
	}

	// My edit is reported; skipping hides the change, a later one is offered.
	if err := os.WriteFile(routine, []byte(digest("2.0", "My own")), 0o600); err != nil {
		t.Fatal(err)
	}
	l.put("personal/digest.prompt", digest("3.0", "Post later"), 0o600)
	if u := l.outdated(false)["digest"]; u.Edited != engine.EditedYes {
		t.Fatalf("edit not seen: %+v", u)
	}
	l.must("templates", "skip", "digest")
	if ups := l.outdated(false); len(ups) != 0 {
		t.Fatalf("skipped, still offered: %+v", ups)
	}
	if u, ok := l.outdated(true)["digest"]; !ok || !u.Skipped {
		t.Fatalf("--all: %+v", u)
	}
	l.put("personal/digest.prompt", digest("4.0", "Post again"), 0o600)
	if _, ok := l.outdated(false)["digest"]; !ok {
		t.Fatal("a later change isn't offered")
	}

	// A new required parameter blocks it.
	l.put("personal/digest.prompt", strings.Replace(digest("5.0", "Post {{team}}"), "  - {name: channel, type: string}\n",
		"  - {name: channel, type: string}\n  - {name: team, type: string}\n", 1), 0o600)
	if u := l.outdated(false)["digest"]; !strings.Contains(u.Blocked, "team") {
		t.Fatalf("blocked: %+v", u)
	}
	if _, err := l.run("templates", "override", "digest"); err == nil {
		t.Fatal("a blocked one was overridden")
	}

	// Deleting it forgets where it came from.
	l.must("routines", "delete", "digest")
	if ups := l.outdated(true); len(ups) != 0 {
		t.Fatalf("deleted, still offered: %+v", ups)
	}
}

// A routine installed before installs were recorded is matched to its
// template: as the template's content, it's recorded as current; otherwise
// offered, possibly edited.
func TestTemplateUpdatesMatched(t *testing.T) {
	l := newTemplateLab(t)
	l.put("personal/digest.prompt", digest("1.0", "Post"), 0o600)
	l.put("personal/notes.prompt", digest("1.0", "Notes"), 0o600)
	rdir := filepath.Join(filepath.Dir(l.cfg), "routines")
	_ = os.MkdirAll(rdir, 0o700)
	_ = os.WriteFile(filepath.Join(rdir, "digest.prompt"), []byte(digest("1.0", "Post")), 0o600)
	_ = os.WriteFile(filepath.Join(rdir, "notes.prompt"), []byte(digest("0.9", "My notes")), 0o600)
	if err := l.app().RecordMatchedInstalls(); err != nil {
		t.Fatal(err)
	}
	ups := l.outdated(false)
	if _, ok := ups["digest"]; ok {
		t.Fatalf("the template's own content offered: %+v", ups)
	}
	if u, ok := ups["notes"]; !ok || u.Edited != engine.EditedUnknown {
		t.Fatalf("notes: %+v", ups)
	}
	l.put("personal/digest.prompt", digest("2.0", "Post now"), 0o600)
	if u := l.outdated(false)["digest"]; u.Edited != engine.EditedNo || u.InstalledVersion != "1.0" {
		t.Fatalf("digest: %+v", u)
	}
}

// A template that has since gained its version line still matches the
// copy installed before it, and a project's template is preferred over a
// general one of the same name.
func TestTemplateUpdatesMatchedBeforeVersions(t *testing.T) {
	l := newTemplateLab(t)
	plain := "---\nautomator.outcome: {done: Posted., nothingToDo: []}\n---\nPost.\n"
	versioned := "---\nautomator.version: \"1.0\"\nautomator.outcome: {done: Posted., nothingToDo: []}\n---\nPost.\n"
	l.put("projects/acme/standup.prompt", versioned, 0o600)
	l.put("general/standup.prompt", strings.Replace(versioned, "Post.", "Post it.", 1), 0o600)
	rdir := filepath.Join(filepath.Dir(l.cfg), "routines")
	_ = os.MkdirAll(rdir, 0o700)
	_ = os.WriteFile(filepath.Join(rdir, "standup.prompt"), []byte(plain), 0o600)
	if err := l.app().RecordMatchedInstalls(); err != nil {
		t.Fatal(err)
	}
	if ups := l.outdated(true); len(ups) != 0 {
		t.Fatalf("the template's content but its version line offered: %+v", ups)
	}
	l.put("projects/acme/standup.prompt", strings.Replace(versioned, "Post.", "Post now.", 1), 0o600)
	if u := l.outdated(false)["standup"]; u.Template != "projects/acme/standup" || u.Edited != engine.EditedNo {
		t.Fatalf("standup: %+v", u)
	}
}

// A hook and its watcher are overridden from their templates, the watcher
// first when both changed; built again from source, their values kept.
func TestTemplateUpdatesFolders(t *testing.T) {
	l := newTemplateLab(t)
	l.put("github/src/gh.sh", "#!/bin/sh\necho v1\n", 0o755)
	l.put("github/gh-reviews/template.yaml", "version: \"1.0\"\nparams: [{name: login, type: string}]\nbuild: {command: [cp, gh.sh, \"{{output}}\"], dir: ../src}\n", 0o600)
	l.put("github/gh-reviews/watcher.yaml", "description: PRs\nexecutable: ./gh\nmode: poll\ninterval: 5m\nargs: [author, \"{{login}}\"]\nevents: {review.requested: A review is requested}\n", 0o600)
	l.put("github/pr-review-requested/template.yaml", "version: \"1.0\"\nbuild: {command: [cp, gh.sh, \"{{output}}\"], dir: ../src}\n", 0o600)
	l.put("github/pr-review-requested/hook.yaml", "description: Review requests\nexecutable: ./gh\nwrites: false\nargs: [notify]\non: {watcher: gh-reviews, type: review.requested}\n", 0o600)
	l.must("templates", "install", "github/pr-review-requested", "--set", "login=me")
	if ups := l.outdated(false); len(ups) != 0 {
		t.Fatalf("just installed, offered: %+v", ups)
	}
	// The shared source changed: both are offered, the hook with its watcher.
	l.put("github/src/gh.sh", "#!/bin/sh\necho v2\n", 0o755)
	ups := l.outdated(false)
	if h, w := ups["pr-review-requested"], ups["gh-reviews"]; len(h.With) != 1 || h.With[0] != "gh-reviews" || w.Kind != "watcher" {
		t.Fatalf("offered: %+v", ups)
	}
	l.must("templates", "override", "pr-review-requested")
	if ups := l.outdated(false); len(ups) != 0 {
		t.Fatalf("overridden, still offered: %+v", ups)
	}
	data := filepath.Dir(l.cfg)
	for _, exe := range []string{"hooks/pr-review-requested/gh", "watchers/gh-reviews/gh"} {
		if b, _ := os.ReadFile(filepath.Join(data, exe)); !strings.Contains(string(b), "v2") {
			t.Fatalf("%s not rebuilt: %s", exe, b)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(data, "watchers/gh-reviews/watcher.yaml")); !strings.Contains(string(b), "[author, \"me\"]") {
		t.Fatalf("the watcher's value is lost:\n%s", b)
	}
	// Untrusted before, untrusted after.
	a := l.app()
	w, err := a.FindWatcher("gh-reviews")
	if err != nil {
		t.Fatal(err)
	}
	if why, _ := a.WatcherUntrusted(w); why == "" {
		t.Fatal("an untrusted watcher became trusted")
	}
}
