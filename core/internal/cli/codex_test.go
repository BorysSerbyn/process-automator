package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BorysSerbyn/process-automator/core/internal/engine/enginetest"
	"github.com/BorysSerbyn/process-automator/core/internal/usage"
)

// On codex, setup has the backend step and none of Claude Code's (plan
// usage, MCP connection); there's no usage limit; Codex signs in on its own.
func TestCodexBackend(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	enginetest.Set(t, cfg, "llm.backend", "codex")
	a := enginetest.Load(t, cfg)
	// Codex isn't installed, and the saved login PATH is fresh (no login shell is run).
	t.Setenv("PATH", t.TempDir())
	_ = os.WriteFile(a.Layout.LoginPath(), []byte(t.TempDir()), 0o600)

	out, err := cli(t, cfg, "setup", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var got SetupOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	for _, s := range got.Steps {
		switch s.Step {
		case stepBackend:
			if s.Done || !strings.Contains(s.Status, "Runs with codex (experimental, untested), but Codex isn't installed") || s.SignIn != "" ||
				!strings.Contains(s.Ask, "codex (experimental, untested) = headless OpenAI Codex CLI") {
				t.Errorf("backend step: %+v", s)
			}
		case stepUsage, stepMCP:
			t.Errorf("a Claude Code step on codex: %+v", s)
		}
	}

	out, err = cli(t, cfg, "usage", "--json")
	var r usage.Report
	if err != nil || json.Unmarshal([]byte(out), &r) != nil || r.Kind != usage.KindNone || r.Over {
		t.Errorf("usage: %s %v", out, err)
	}
	if out, _ := cli(t, cfg, "usage"); !strings.Contains(out, "No usage limit") {
		t.Errorf("usage: %s", out)
	}
	if out, _ := cli(t, cfg, "settings", "list"); !strings.Contains(out, "[only used with llm.backend claude-code]") {
		t.Errorf("llm.usage-limit should be marked as not applying:\n%s", out)
	}
	if _, err := cli(t, cfg, "auth", "login", "codex"); err == nil || !strings.Contains(err.Error(), "codex login") {
		t.Errorf("auth login codex: %v", err)
	}
}
