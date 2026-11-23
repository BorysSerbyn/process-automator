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
)

// A hook template installs its watcher's template with it (built, its
// parameter filled in), both off and untrusted, as hooks add and watchers
// add would; a program template as routines create --kind program would.
func TestTemplatesInstallFolders(t *testing.T) {
	tdir := t.TempDir()
	put := func(path, body string, mode os.FileMode) {
		p := filepath.Join(tdir, path)
		_ = os.MkdirAll(filepath.Dir(p), 0o700)
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	put("github/src/gh.sh", "#!/bin/sh\necho built\n", 0o755)
	put("github/gh-reviews/template.yaml", "params: [{name: login, type: string}]\nbuild: {command: [cp, gh.sh, \"{{output}}\"], dir: ../src}\n", 0o600)
	put("github/gh-reviews/watcher.yaml", "description: PRs\nexecutable: ./gh\nmode: poll\ninterval: 5m\nargs: [author, \"{{login}}\"]\nevents: {review.requested: A review is requested}\n", 0o600)
	put("github/pr-review-requested/template.yaml", "build: {command: [cp, gh.sh, \"{{output}}\"], dir: ../src}\n", 0o600)
	put("github/pr-review-requested/hook.yaml", "description: Review requests\nexecutable: ./gh\nwrites: false\nargs: [notify]\non: {watcher: gh-reviews, type: review.requested}\n", 0o600)
	put("personal/cleaner/template.yaml", "description: Cleans up\n", 0o600)
	put("personal/cleaner/program.yaml", "executable: ./clean\nwrites: true\n", 0o600)
	put("personal/cleaner/clean", "#!/bin/sh\ntrue\n", 0o755)
	put("personal/lonely/template.yaml", "description: A watcher no hook uses\n", 0o600)
	put("personal/lonely/watcher.yaml", "executable: ./w\nmode: poll\ninterval: 5m\nevents: {x.y: An x}\n", 0o600)
	put("personal/lonely/w", "#!/bin/sh\ntrue\n", 0o755)
	old := product.TemplatesDir
	product.TemplatesDir = tdir
	t.Cleanup(func() { product.TemplatesDir = old })

	cfg := filepath.Join(t.TempDir(), "config.yaml")
	run := func(args ...string) (string, error) {
		root := NewRoot(nil)
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetIn(strings.NewReader(""))
		root.SetArgs(append(args, "--config", cfg))
		err := root.Execute()
		return out.String(), err
	}
	// The hook and its watcher are one entry, the hook's, with both's
	// parameters; a watcher no hook uses is its own.
	list := func() map[string]engine.TemplateInfo {
		t.Helper()
		var rows []engine.TemplateInfo
		out, _ := run("templates", "list", "--json")
		if err := json.Unmarshal([]byte(out), &rows); err != nil {
			t.Fatalf("list: %v %s", err, out)
		}
		byID := map[string]engine.TemplateInfo{}
		for _, r := range rows {
			byID[r.ID] = r
		}
		return byID
	}
	rows := list()
	hook, listed := rows["github/pr-review-requested"]
	if _, watcher := rows["github/gh-reviews"]; watcher || !listed || len(rows) != 3 {
		t.Fatalf("listed %v", rows)
	}
	if w := hook.WithWatcher; w == nil || w.ID != "github/gh-reviews" || w.Installed || len(hook.Params) != 1 || hook.Params[0].Name != "login" {
		t.Fatalf("hook: %+v %+v", hook, hook.WithWatcher)
	}
	if _, ok := rows["personal/lonely"]; !ok {
		t.Fatal("a watcher no hook uses isn't listed")
	}
	if out, _ := run("templates", "list"); !strings.Contains(out, "comes with the watcher gh-reviews") {
		t.Fatalf("list: %s", out)
	}
	if _, err := run("templates", "install", "github/pr-review-requested"); err == nil || !strings.Contains(err.Error(), "login") {
		t.Fatalf("its watcher's required parameter: %v", err)
	}
	out, err := run("templates", "install", "github/pr-review-requested", "--set", "login=rick", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var got InstallOutput
	if i := strings.Index(out, "{"); i < 0 || json.Unmarshal([]byte(out[i:]), &got) != nil || got.Kind != "hook" ||
		got.Routine != "pr-review-requested" || len(got.Also) != 1 || got.Also[0].Name != "gh-reviews" || got.Also[0].Kind != "watcher" {
		t.Fatalf("%s", out)
	}
	data := filepath.Dir(cfg)
	desc, _ := os.ReadFile(filepath.Join(data, "watchers", "gh-reviews", "watcher.yaml"))
	exe, _ := os.ReadFile(filepath.Join(data, "watchers", "gh-reviews", "gh"))
	if !strings.Contains(string(desc), `args: [author, "rick"]`) || string(exe) != "#!/bin/sh\necho built\n" {
		t.Fatalf("watcher: %s / %s", desc, exe)
	}
	if out, _ := run("hooks", "list"); !strings.Contains(out, "pr-review-requested") {
		t.Fatalf("hooks: %s", out)
	}
	if out, _ := run("hooks", "show", "pr-review-requested", "--json"); !strings.Contains(out, `"enabled": false`) && !strings.Contains(out, `"trusted": false`) {
		t.Fatalf("hook on or trusted: %s", out)
	}
	if _, err := run("templates", "install", "github/pr-review-requested", "--as", "again"); err != nil {
		t.Fatalf("a second hook on the installed watcher: %v", err)
	}
	if out, err := run("templates", "install", "personal/cleaner"); err != nil || !strings.Contains(out, "routines trust cleaner --resume") {
		t.Fatalf("program: %v %s", err, out)
	}
	rows = list()
	if h := rows["github/pr-review-requested"]; !h.Installed || h.WithWatcher == nil || !h.WithWatcher.Installed || len(h.Params) != 0 {
		t.Fatalf("installed hook: %+v %+v", h, h.WithWatcher)
	}
	if !rows["personal/cleaner"].Installed {
		t.Error("the program isn't installed")
	}
	// A watcher template is still installed by its ID, alone.
	if _, err := run("templates", "install", "github/gh-reviews", "--as", "gh-other", "--set", "login=morty"); err != nil {
		t.Fatalf("a watcher by its ID: %v", err)
	}
}
