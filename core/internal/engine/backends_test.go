package engine

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/usage"
)

// codexOnPath puts a stand-in codex on PATH (it's never run: the sign-in
// check is replaced), and makes the sign-in check report problem.
func codexOnPath(t *testing.T, problem string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("a stand-in codex needs a POSIX file")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	orig := CodexLoginStatus
	CodexLoginStatus = func(context.Context, string) (string, error) { return problem, nil }
	codexLogin = loginCache{}
	t.Cleanup(func() { CodexLoginStatus, codexLogin = orig, loginCache{} })
}

func useCodex(t *testing.T, a *App) {
	t.Helper()
	if _, err := (settings.Store{Path: a.Layout.Config()}).Set("llm.backend", "codex"); err != nil {
		t.Fatal(err)
	}
}

func TestCodexReady(t *testing.T) {
	a := testApp(t)
	useCodex(t, a)
	_ = os.WriteFile(a.Layout.LoginPath(), []byte(t.TempDir()), 0o600) // a saved login PATH: not looked up again

	t.Setenv("PATH", t.TempDir())
	if b, err := a.BackendReady(); err != nil || !strings.Contains(b.Problem, "Codex isn't installed") || b.SignIn != "" {
		t.Errorf("not installed: %+v %v", b, err)
	}

	codexOnPath(t, "Not logged in")
	if b, _ := a.BackendReady(); !strings.Contains(b.Problem, "isn't signed in (Not logged in)") || !strings.Contains(b.Problem, "codex login") {
		t.Errorf("not signed in: %+v", b)
	}
	codexOnPath(t, "")
	if b, _ := a.BackendReady(); b.Problem != "" || b.Status() != "Runs with codex (experimental, untested), ready" {
		t.Errorf("ready: %+v", b)
	}
	if _, err := a.NewBackend(); err != nil {
		t.Errorf("new backend: %v", err)
	}
}

// Codex has no usage limit in the app: neither the Claude plan's nor the API
// budgets apply.
func TestCodexUsage(t *testing.T) {
	a := testApp(t)
	useCodex(t, a)
	r, err := a.Usage(context.Background(), time.Now())
	if err != nil || r.Kind != usage.KindNone || r.Over || r.Windows == nil {
		t.Fatalf("usage: %+v %v", r, err)
	}
	for _, key := range []string{"llm.usage-limit", "llm.budget.window-usd"} {
		if s, _ := settings.Lookup(key); s.Backend == "" || s.Backend == "codex" {
			t.Errorf("%s applies on codex", key)
		}
	}
}

// Each backend checks builtinTools in its own names: a routine's Claude Code
// tools are a problem on codex, and Codex's fine.
func TestBuiltinToolsPerBackend(t *testing.T) {
	a := testApp(t)
	dir := a.Layout.Routines()
	_ = os.MkdirAll(dir, 0o700)
	write := func(name, tools string) routines.Routine {
		src := "---\nautomator.builtinTools: [" + tools + "]\n---\nGo.\n"
		_ = os.WriteFile(filepath.Join(dir, name+".prompt"), []byte(src), 0o600)
		set, err := routines.Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		r, err := set.Find(name)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	claude, codex := write("claudeish", "Read, Bash"), write("codexish", "shell, web_search")
	problems := func(r routines.Routine) string {
		var out []string
		for _, p := range a.Assess(r).Problems {
			out = append(out, p.Text)
		}
		return strings.Join(out, "; ")
	}
	if _, err := (settings.Store{Path: a.Layout.Config()}).Set("llm.backend", "claude-code"); err != nil {
		t.Fatal(err)
	}
	if p := problems(claude); p != "" {
		t.Errorf("claude-code, its tools: %s", p)
	}
	if p := problems(codex); !strings.Contains(p, "isn't a Claude Code tool name") {
		t.Errorf("claude-code, Codex's tools: %q", p)
	}
	useCodex(t, a)
	if p := problems(codex); p != "" {
		t.Errorf("codex, its tools: %s", p)
	}
	if p := problems(claude); !strings.Contains(p, "on the codex backend") || !strings.Contains(p, "isn't a Codex built-in tool") {
		t.Errorf("codex, Claude Code's tools: %q", p)
	}
}
