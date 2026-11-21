package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
)

func TestTemplatesInstall(t *testing.T) {
	tdir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tdir, "personal"), 0o700)
	_ = os.WriteFile(filepath.Join(tdir, "personal", "digest.prompt"), []byte(`---
description: Finds open requests
automator.schedule: "0 8 * * 1-5"
automator.parameters:
  - {name: lookback, type: duration, default: 72h}
  - {name: channel, type: string, description: Chat channel ID}
---
Look back {{lookback}} in {{channel}}.`), 0o600)
	old := product.TemplatesDir
	product.TemplatesDir = tdir
	t.Cleanup(func() { product.TemplatesDir = old })

	cfg := filepath.Join(t.TempDir(), "config.yaml")
	run := func(args ...string) (string, error) {
		root := NewRoot(nil)
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetIn(strings.NewReader("")) // not a terminal
		root.SetArgs(append(args, "--config", cfg))
		err := root.Execute()
		return out.String(), err
	}
	installed := filepath.Join(filepath.Dir(cfg), "routines", "digest.prompt")

	// A required parameter without a value, and no terminal to ask on: nothing is written.
	if _, err := run("templates", "install", "personal/digest"); err == nil || !strings.Contains(err.Error(), "channel") {
		t.Fatalf("missing required parameter: %v", err)
	}
	if _, err := os.Stat(installed); !os.IsNotExist(err) {
		t.Fatal("a failed install must not leave a routine behind")
	}
	if _, err := run("templates", "install", "personal/digest", "--set", "lookback=soon", "--set", "channel=C1"); err == nil {
		t.Fatal("an invalid value must be rejected before installing")
	}

	if out, err := run("templates", "install", "personal/digest", "--set", "channel=C1"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if _, err := run("templates", "install", "personal/digest", "--set", "channel=C2"); err == nil {
		t.Fatal("installing over an existing routine must fail")
	}
	if out, err := run("templates", "install", "personal/digest", "--as", "digest-team", "--set", "channel=C2"); err != nil {
		t.Fatalf("install under another name: %v\n%s", err, out)
	}

	// Values are per-routine settings; defaults fill the rest.
	a, err := loadTest(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	set, _ := routines.Load(a.Layout.Routines())
	for name, channel := range map[string]string{"digest": "C1", "digest-team": "C2"} {
		r, err := set.Find(name)
		if err != nil {
			t.Fatal(err)
		}
		c, err := a.RoutineConfig(r)
		if err != nil || c.Params["channel"] != channel || c.Params["lookback"] != "72h" || c.Schedule != "0 8 * * 1-5" {
			t.Fatalf("%s: %+v %v", name, c, err)
		}
	}
	out, _ := run("templates", "list")
	if !strings.Contains(out, "personal/digest") || !strings.Contains(out, "channel*") || !strings.Contains(out, "true") {
		t.Fatalf("list:\n%s", out)
	}
}

// A template for some backends only is listed, marked, and not offered by
// setup on another backend; installing it there is refused, with how to
// switch; on its backend it installs.
func TestTemplateBackends(t *testing.T) {
	tdir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tdir, "personal"), 0o700)
	_ = os.WriteFile(filepath.Join(tdir, "personal", "sweep.prompt"), []byte(
		"---\nautomator.backends: [claude-code]\nautomator.builtinTools: [Bash]\n---\nSweep.\n"), 0o600)
	old := product.TemplatesDir
	product.TemplatesDir = tdir
	t.Cleanup(func() { product.TemplatesDir = old })
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if _, err := cli(t, cfg, "settings", "set", "llm.backend", "api"); err != nil {
		t.Fatal(err)
	}
	out, _ := cli(t, cfg, "templates", "list", "--json")
	var rows []TemplateInfo
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 1 || rows[0].SupportsBackend ||
		len(rows[0].Backends) != 1 || rows[0].Backends[0] != "claude-code" {
		t.Fatalf("list on api: %v\n%s", err, out)
	}
	a, err := loadTest(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := templatesOffered(a); n != 0 {
		t.Errorf("setup offers %d templates on a backend they don't support", n)
	}
	_, err = cli(t, cfg, "templates", "install", "personal/sweep")
	if err == nil || !strings.Contains(err.Error(), "works only with the claude-code backend, and llm.backend is api") ||
		!strings.Contains(err.Error(), "settings set llm.backend claude-code") {
		t.Fatalf("install on api: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(cfg), "routines", "sweep.prompt")); !os.IsNotExist(err) {
		t.Fatal("a refused install left a routine behind")
	}
	if _, err := cli(t, cfg, "settings", "set", "llm.backend", "claude-code"); err != nil {
		t.Fatal(err)
	}
	if out, err := cli(t, cfg, "templates", "install", "personal/sweep"); err != nil {
		t.Fatalf("install on claude-code: %v\n%s", err, out)
	}
}

// With no templates (an empty or missing folder), listing shows none,
// installing says there are none, and setup's optional templates step offers
// writing a routine instead.
func TestNoTemplates(t *testing.T) {
	for name, tdir := range map[string]string{"empty": t.TempDir(), "missing": filepath.Join(t.TempDir(), "missing")} {
		t.Run(name, func(t *testing.T) {
			old := product.TemplatesDir
			product.TemplatesDir = tdir
			t.Cleanup(func() { product.TemplatesDir = old })
			cfg := filepath.Join(t.TempDir(), "config.yaml")

			out, err := cli(t, cfg, "templates", "list", "--json")
			if err != nil || strings.TrimSpace(out) != "[]" {
				t.Fatalf("templates list: %v\n%s", err, out)
			}
			if _, err := cli(t, cfg, "templates", "install", "personal/digest"); err == nil || !strings.Contains(err.Error(), "no templates") {
				t.Fatalf("install with no templates: %v", err)
			}
			out, err = cli(t, cfg, "setup", "--json")
			if err != nil {
				t.Fatalf("setup: %v\n%s", err, out)
			}
			var got SetupOutput
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatal(err)
			}
			st := got.Steps[0]
			if st.Step != stepTemplates || st.Done || !st.Optional || !strings.Contains(st.Ask, "no templates") ||
				len(st.Commands) != 1 || !strings.Contains(st.Commands[0], "routines create") {
				t.Fatalf("templates step: %+v", st)
			}
		})
	}
}
