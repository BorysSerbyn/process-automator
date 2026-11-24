package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The implementations are kept oldest first, each from a later version.
func TestClaudeCodeProtocolsOrdered(t *testing.T) {
	for i := 1; i < len(claudeCodeProtocols); i++ {
		if !claudeCodeProtocols[i-1].Since().Less(claudeCodeProtocols[i].Since()) {
			t.Fatalf("implementation %d (%s) isn't newer than the one before (%s)",
				i, claudeCodeProtocols[i].Since(), claudeCodeProtocols[i-1].Since())
		}
	}
}

// A Claude Code gets the newest implementation at or below its version, and
// one older than every implementation is refused.
func TestClaudeCodeFor(t *testing.T) {
	saved := claudeCodeProtocols
	t.Cleanup(func() { claudeCodeProtocols = saved })
	older, newer := fakeProtocol{since: "2.0.0"}, fakeProtocol{since: "2.2.0"}
	claudeCodeProtocols = []ClaudeCodeProtocol{older, newer}
	for v, want := range map[string]ClaudeCodeProtocol{"2.0.0": older, "2.1.999": older, "2.2.0": newer, "3.0": newer} {
		if got, err := ClaudeCodeFor(mustVersion(v)); err != nil || got != want {
			t.Errorf("%s: got %v, %v", v, got, err)
		}
	}
	_, err := ClaudeCodeFor(mustVersion("1.9.9"))
	var old *TooOldError
	if !errors.As(err, &old) || old.Need.String() != "2.0.0" || !strings.Contains(err.Error(), "claude update") {
		t.Fatalf("an older Claude Code: %v", err)
	}
	if NewestClaudeCode() != newer || OldestClaudeCode() != older {
		t.Fatal("newest and oldest")
	}
}

type fakeProtocol struct {
	claudeCode2_1_331
	since string
}

func (f fakeProtocol) Since() Version { return mustVersion(f.since) }

// The user-scope MCP servers are read from Claude Code's config file.
func TestClaudeCodeMCPServer(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	p, path := NewestClaudeCode(), filepath.Join(dir, ".claude.json")
	if _, ok, err := p.MCPServer("app"); ok || err != nil {
		t.Errorf("no config file: %v %v", ok, err)
	}
	if problem, _ := ClaudeMCPProblem(p, "app"); problem == "" {
		t.Error("no server, no problem")
	}
	_ = os.WriteFile(path, []byte(`{"projects":{"/x":{"mcpServers":{"app":{"command":"/p"}}}},`+
		`"mcpServers":{"app":{"type":"stdio","command":"/bin/sh","args":["mcp","serve"],"env":{}}}}`), 0o600)
	s, ok, err := p.MCPServer("app")
	if err != nil || !ok || s.Command != "/bin/sh" || len(s.Args) != 2 {
		t.Errorf("registered: %+v %v %v", s, ok, err)
	}
	if problem, err := ClaudeMCPProblem(p, "app"); problem != "" || err != nil {
		t.Errorf("registered, yet: %q %v", problem, err)
	}
	if _, ok, _ := p.MCPServer("other"); ok {
		t.Error("found a server that isn't there")
	}
	_ = os.WriteFile(path, []byte(`{"mcpServers":{"app":{"command":"/gone/app"}}}`), 0o600)
	if problem, _ := ClaudeMCPProblem(p, "app"); problem == "" {
		t.Error("a server whose command is gone, no problem")
	}
	_ = os.WriteFile(path, []byte(`{`), 0o600)
	if _, _, err := p.MCPServer("app"); err == nil {
		t.Error("a broken config file read as fine")
	}
}

// The status line picks the implementation by the version in its input, and
// reads the plan's windows from it.
func TestClaudeCodeStatusLine(t *testing.T) {
	in := []byte(`{"version":"` + NewestClaudeCode().Since().String() + `","rate_limits":{"five_hour":{"used_percentage":42,"resets_at":1700000000},"seven_day":{"used_percentage":7}}}`)
	p, err := ClaudeCodeForStatusLine(in)
	if err != nil || p != NewestClaudeCode() {
		t.Fatalf("%v, %v", p, err)
	}
	session, weekly, err := p.StatusLineUsage(in)
	if err != nil || session == nil || session.UsedPercent != 42 || session.ResetsAt.Unix() != 1700000000 || weekly != nil {
		t.Fatalf("session %+v weekly %+v: %v", session, weekly, err)
	}
	if _, err := ClaudeCodeForStatusLine([]byte(`{"version":"0.1.0"}`)); err == nil {
		t.Fatal("a Claude Code older than every implementation got one")
	}
	if p, _ := ClaudeCodeForStatusLine([]byte(`{}`)); p != NewestClaudeCode() {
		t.Fatal("no version: not the newest")
	}
	f, err := p.StatusLine()
	if err != nil || f.Key != "statusLine" || f.CommandField != "command" {
		t.Fatalf("format %+v %v", f, err)
	}
}
