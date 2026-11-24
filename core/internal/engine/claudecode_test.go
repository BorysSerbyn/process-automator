package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/statusline"
)

// withClaudeVersion makes the stand-in claude report v ("" fails) for one test.
func withClaudeVersion(t *testing.T, v string) {
	t.Helper()
	saved := testClaudeVersion
	testClaudeVersion = v
	undo := agent.SetVersionForTests(testVersion)
	t.Cleanup(func() { undo(); testClaudeVersion = saved })
}

func onClaudeCode(t *testing.T) *App {
	t.Helper()
	a := testApp(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	if _, err := (settings.Store{Path: a.Layout.Config()}).Set("llm.backend", "claude-code"); err != nil {
		t.Fatal(err)
	}
	return a
}

// The installed Claude Code's version picks its implementation; one too old,
// one whose --version fails, or none is the backend's problem (said once:
// the MCP step leaves it to the backend).
func TestClaudeCodeReadiness(t *testing.T) {
	for _, c := range []struct {
		name, version, problem string
	}{
		{"supported", agent.NewestClaudeCode().Since().String(), ""},
		{"newer", "999.0.0", ""},
		{"too old", "0.0.1", "is too old"},
		{"version fails", "", "can't be used"},
	} {
		t.Run(c.name, func(t *testing.T) {
			withClaudeVersion(t, c.version)
			a := onClaudeCode(t)
			st, err := a.BackendReady()
			if err != nil {
				t.Fatal(err)
			}
			if c.problem == "" && st.Problem != "" || !strings.Contains(st.Problem, c.problem) {
				t.Fatalf("problem %q, want %q", st.Problem, c.problem)
			}
			_, shown, err := a.MCPStep()
			if err != nil || shown != (c.problem == "") {
				t.Fatalf("MCP step shown %v (%v)", shown, err)
			}
		})
	}
	t.Run("not installed", func(t *testing.T) {
		a := onClaudeCode(t)
		t.Setenv("PATH", t.TempDir())
		if st, _ := a.BackendReady(); !strings.Contains(st.Problem, "isn't installed") {
			t.Fatalf("problem %q", st.Problem)
		}
	})
}

// Uninstalling puts my status line back even when Claude Code is gone.
func TestUninstallClaudeStatusLineWithoutClaude(t *testing.T) {
	a := onClaudeCode(t)
	f, err := agent.NewestClaudeCode().StatusLine()
	if err != nil {
		t.Fatal(err)
	}
	mine := `{"statusLine":{"type":"command","command":"mine.sh"}}`
	if err := os.MkdirAll(filepath.Dir(f.SettingsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(f.SettingsPath, []byte(mine), 0o600)
	if err := statusline.Install(f, a.Layout.StatusLine(), "/gone/app "+statusline.Marker); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir()) // no claude
	if err := UninstallClaudeStatusLine(a); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(f.SettingsPath)
	if !strings.Contains(string(b), "mine.sh") || strings.Contains(string(b), statusline.Marker) {
		t.Fatalf("settings after uninstall: %s", b)
	}
}
