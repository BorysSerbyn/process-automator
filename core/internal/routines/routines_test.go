package routines

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/firebase/genkit/go/genkit"

	"github.com/BorysSerbyn/process-automator/core/internal/trigger"
)

func TestLoadFixtureFolder(t *testing.T) {
	set, err := Load("testdata/routines")
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, r := range set.Routines {
		names = append(names, r.Name)
	}
	if got := strings.Join(names, ","); got != "no-schedule,scrum" {
		t.Fatalf("routines = %s", got)
	}
	s, _ := set.Find("scrum")
	if s.File["schedule"] != "15 9 * * 1-5" || s.File["staleness"] != "15m" || len(s.Servers) != 3 || len(s.Tools) != 2 {
		t.Fatalf("scrum parsed wrong: %+v", s)
	}
	if n, _ := set.Find("no-schedule"); n.Scheduled() {
		t.Fatal("no-schedule should be manual-only")
	}

	if set.Partials["about-me"] != "I am a test user." || len(set.Partials) != 1 {
		t.Fatalf("partials = %v", set.Partials)
	}

	// Broken files are reported individually and skipped; they don't break the rest.
	var broken []string
	for _, e := range set.Errors {
		broken = append(broken, filepath.Base(e.Path))
	}
	sort.Strings(broken)
	want := "_memories.prompt,bad-cron.prompt,scrum.v2.prompt,typo.prompt"
	if got := strings.Join(broken, ","); got != want {
		t.Fatalf("errors = %s, want %s", got, want)
	}
	if _, err := set.Find("typo"); err == nil || !strings.Contains(err.Error(), "schedul") {
		t.Fatalf("Find(broken) should return its error, got %v", err)
	}
	if _, err := set.Find("nope"); err == nil || !strings.Contains(err.Error(), "scrum") {
		t.Fatalf("Find(unknown) should list routines, got %v", err)
	}
}

func TestLoadMissingDirIsEmpty(t *testing.T) {
	set, err := Load(filepath.Join(t.TempDir(), "nope"))
	if err != nil || len(set.Routines) != 0 || len(set.Errors) != 0 {
		t.Fatalf("got %+v, %v", set, err)
	}
}

func TestCRLFAndToolsWithoutServers(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "win.prompt"), []byte("---\r\nautomator.servers: [notes]\r\ntools: [list_notes]\r\n---\r\nHi\r\n"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "noservers.prompt"), []byte("---\ntools: [a]\n---\nhi"), 0o600)
	set, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Routines) != 1 || set.Routines[0].Tools[0] != "list_notes" {
		t.Fatalf("CRLF routine not parsed: %+v", set.Routines)
	}
	if len(set.Errors) != 1 || !strings.Contains(set.Errors[0].Error(), "servers") {
		t.Fatalf("want tools-without-servers error, got %v", set.Errors)
	}
}

func TestPreviousWorkday(t *testing.T) {
	for in, want := range map[string]string{"2026-11-17": "2026-11-16", "2026-11-16": "2026-11-13", "2026-11-15": "2026-11-13"} {
		now, _ := time.ParseInLocation("2006-01-02", in, time.Local)
		if got := PreviousWorkday(now.Add(9 * time.Hour)).Format("2006-01-02"); got != want {
			t.Errorf("PreviousWorkday(%s) = %s, want %s", in, got, want)
		}
	}
}

// Every template the repo ships must load cleanly, include the memories
// partial, and render with the standard inputs through Genkit.
func TestTemplates(t *testing.T) {
	dirs := templateDirs("../../../templates")
	if len(dirs) == 0 {
		t.Skip("the repo ships no templates")
	}
	for _, dir := range dirs {
		t.Run(strings.TrimPrefix(dir, "../../../templates/"), func(t *testing.T) { checkTemplates(t, dir) })
	}
}

// The template checks themselves, on a template that passes them.
func TestTemplateChecks(t *testing.T) {
	dirs := templateDirs("testdata/templates")
	if len(dirs) == 0 {
		t.Fatal("no template fixtures")
	}
	for _, dir := range dirs {
		checkTemplates(t, dir)
	}
}

// templateDirs are the folders under root holding templates. Templates are
// grouped in tag folders (e.g. projects/acme); each folder holding .prompt
// files is a routines folder of its own.
func templateDirs(root string) []string {
	var dirs []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, Ext) && !slices.Contains(dirs, filepath.Dir(path)) {
			dirs = append(dirs, filepath.Dir(path))
		}
		return nil
	})
	return dirs
}

func checkTemplates(t *testing.T, dir string) {
	set, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Errors) > 0 {
		t.Fatalf("template errors: %v", set.Errors)
	}
	now := time.Date(2026, 11, 17, 9, 15, 0, 0, time.Local)
	for _, r := range set.Routines {
		if !strings.Contains(r.Source, "{{> memories}}") {
			t.Errorf("%s lacks {{> memories}}", r.Name)
		}
		g := genkit.Init(context.Background())
		for name, src := range set.PromptPartials("MEMORIES-HERE") {
			genkit.DefinePartial(g, name, src)
		}
		p, err := genkit.LoadPromptFromSource(g, r.Source, r.Name, "")
		if err != nil {
			t.Fatalf("%s: load: %v", r.Name, err)
		}
		req, err := p.Render(context.Background(), Inputs(trigger.New(trigger.Schedule, now)))
		if err != nil {
			t.Fatalf("%s: render: %v", r.Name, err)
		}
		var text strings.Builder
		for _, m := range req.Messages {
			for _, c := range m.Content {
				text.WriteString(c.Text)
			}
		}
		got := text.String()
		if !strings.Contains(got, "MEMORIES-HERE") {
			t.Errorf("%s: rendered prompt lacks its memories", r.Name)
		}
		// It's anchored in the run's time: today or since (the previous workday).
		if !strings.Contains(got, "Tuesday 2026-11-17") && !strings.Contains(got, "Monday 2026-11-16") {
			t.Errorf("%s: rendered prompt uses neither today nor since", r.Name)
		}
		if strings.Contains(got, "{{") {
			t.Errorf("%s: unrendered template syntax left", r.Name)
		}
	}
}

func TestRoutineFields(t *testing.T) {
	dir := t.TempDir()
	write := func(name, front string) {
		_ = os.WriteFile(filepath.Join(dir, name+".prompt"), []byte("---\nmodel: anthropic/claude-haiku-5-5\n"+front+"---\nhi"), 0o600)
	}
	write("full", "automator.schedule: \"20 9 * * 1-5\"\nautomator.timeout: 90m\nautomator.effort: high\nautomator.enabled: false\n")
	write("badeffort", "automator.effort: extreme\n")
	write("badtimeout", "automator.timeout: soon\n")
	set, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	r, err := set.Find("full")
	if err != nil {
		t.Fatal(err)
	}
	if r.Model != "anthropic/claude-haiku-5-5" || r.File["timeout"] != "90m" || r.File["effort"] != "high" || r.File["enabled"] != false {
		t.Fatalf("parsed: %+v", r)
	}
	defaults := map[string]any{}
	for _, s := range r.Settings() {
		defaults[strings.TrimPrefix(s.Key, "routines.full.")] = s.Default
	}
	if defaults["timeout"] != "90m" || defaults["effort"] != "high" || defaults["staleness"] != "1h" || defaults["enabled"] != false {
		t.Fatalf("settings defaults: %v", defaults)
	}
	for _, bad := range []string{"badeffort", "badtimeout"} {
		if _, err := set.Find(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestParameters(t *testing.T) {
	dir := t.TempDir()
	write := func(name, front string) {
		_ = os.WriteFile(filepath.Join(dir, name+".prompt"), []byte("---\n"+front+"---\nhi"), 0o600)
	}
	write("ok", "automator.parameters:\n  - {name: lookback, type: duration, default: 72h}\n  - {name: tone, type: enum, enum: [brief, full]}\n")
	write("badtype", "automator.parameters:\n  - {name: x, type: color}\n")
	write("baddefault", "automator.parameters:\n  - {name: x, type: duration, default: soon}\n")
	write("reserved", "automator.parameters:\n  - {name: today, type: string}\n")
	write("badname", "automator.parameters:\n  - {name: Look-Back, type: string}\n")
	write("noenum", "automator.parameters:\n  - {name: x, type: enum}\n")
	write("badplatform", "automator.platforms: [windows]\n")
	set, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	r, err := set.Find("ok")
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]any{}
	for _, s := range r.Settings() {
		keys[s.Key] = s.Default
	}
	if keys["routines.ok.params.lookback"] != "72h" || keys["routines.ok.params.tone"] != nil {
		t.Fatalf("parameter settings: %v", keys)
	}
	for _, bad := range []string{"badtype", "baddefault", "reserved", "badname", "noenum", "badplatform"} {
		if _, err := set.Find(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestCreateUpdateDelete(t *testing.T) {
	dir := t.TempDir()
	src := "---\nautomator.servers: [notes]\n---\nDo it. {{> memories}}\n"
	if _, err := Create(dir, "Bad Name", src); err == nil {
		t.Fatal("bad name accepted")
	}
	if _, err := Create(dir, "x", "---\nautomator:\n  servers: [notes]\n---\nhi"); err == nil {
		t.Fatal("nested automator: block accepted (it would be ignored)")
	}
	for _, bad := range []string{"---\n: : :\n---\nhi", "---\nautomator.servers: [x]\nhi"} {
		if _, err := Create(dir, "x", bad); err == nil {
			t.Fatalf("broken frontmatter accepted: %q", bad)
		}
	}
	if _, err := Create(dir, "x", "---\nautomator.nope: 1\n---\nhi"); err == nil {
		t.Fatal("invalid routine written")
	}
	if _, err := os.Stat(filepath.Join(dir, "x.prompt")); !os.IsNotExist(err) {
		t.Fatal("invalid create left a file")
	}
	r, err := Create(dir, "x", src)
	if err != nil || r.Name != "x" || len(r.Servers) != 1 {
		t.Fatalf("create: %+v %v", r, err)
	}
	if _, err := Create(dir, "x", src); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("create overwrote: %v", err)
	}
	if _, err := Update(dir, "y", src); err == nil {
		t.Fatal("update created a routine")
	}
	if _, err := Update(dir, "x", "---\nautomator.nope: 1\n---\nhi"); err == nil {
		t.Fatal("invalid update accepted")
	}
	if r, err = Update(dir, "x", strings.Replace(src, "Do it.", "Do it now.", 1)); err != nil || !strings.Contains(r.Source, "now") {
		t.Fatalf("update: %v", err)
	}
	set, _ := Load(dir)
	if len(set.Routines) != 1 || len(set.Errors) != 0 {
		t.Fatalf("leftovers after update: %+v", set)
	}
	if _, err := Delete(dir, "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := Delete(dir, "x"); err == nil {
		t.Fatal("deleted twice")
	}
}

func TestPluginSections(t *testing.T) {
	dir := t.TempDir()
	ok := "---\nautomator.servers: [scripts]\nautomator.plugins:\n  scripts: {sweep: ./sweep.sh}\n---\nGo. {{> memories}}\n"
	r, err := Create(dir, "a", ok)
	if err != nil {
		t.Fatal(err)
	}
	if sec, _ := r.Plugins["scripts"].(map[string]any); sec["sweep"] != "./sweep.sh" {
		t.Fatalf("plugins: %#v", r.Plugins)
	}
	// The dotted form, like every other routine field.
	dotted, err := Parse("d", "---\nautomator.servers: [scripts]\nautomator.plugins.scripts: {sweep: ./sweep.sh}\n---\nGo.")
	if err != nil {
		t.Fatal(err)
	}
	if sec, _ := dotted.Plugins["scripts"].(map[string]any); sec["sweep"] != "./sweep.sh" {
		t.Fatalf("dotted plugins section lost: %#v", dotted.Plugins)
	}
	for _, bad := range []string{
		"---\nautomator.servers: [notes]\nautomator.plugins:\n  scripts: {x: y}\n---\nGo.",
		"---\nautomator.servers: [notes]\nautomator.plugins.scripts: {x: y}\n---\nGo.",
		"---\nautomator.servers: [scripts]\nautomator.plugins.scripts: {x: y}\nautomator.plugins:\n  scripts: {x: z}\n---\nGo.",
		"---\nautomator.servers: [scripts]\nautomator.nope.deep: 1\n---\nGo.",
		"---\nautomator.servers: [scripts]\nautomator.plugins: [scripts]\n---\nGo.",
	} {
		if _, err := Parse("b", bad); err == nil {
			t.Errorf("accepted: %q", bad)
		}
	}
}

// A parameter's required and defaultText are worked out, never written.
func TestParamsComputedFields(t *testing.T) {
	src := "---\nautomator.parameters:\n  - name: channel\n    type: string\n  - name: title\n    type: string\n    default: Daily\n---\nHi.\n"
	r, err := Parse("p", src)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Params[0].Required || r.Params[1].Required || r.Params[1].DefaultText != "Daily" {
		t.Errorf("params: %+v", r.Params)
	}
	if _, err := Parse("p", strings.Replace(src, "type: string\n  - name: title", "type: string\n    required: false\n  - name: title", 1)); err == nil {
		t.Error("a routine file set required")
	}
}
