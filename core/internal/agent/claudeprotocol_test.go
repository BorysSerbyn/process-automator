package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClaudeCodeVersions(t *testing.T) {
	v, err := ParseClaudeCodeVersion("2.1.331 (Claude Code)\n")
	if err != nil || v.String() != "2.1.331" {
		t.Fatalf("parsed %v, %v", v, err)
	}
	if _, err := ParseClaudeCodeVersion("Claude Code"); err == nil {
		t.Fatal("no version parsed as one")
	}
	for _, c := range []struct {
		a, b string
		less bool
	}{{"2.1.9", "2.1.10", true}, {"2.1.10", "2.1.9", false}, {"2.1", "2.1.0", false}, {"2.1", "2.1.1", true}, {"3.0.0", "2.9.9", false}} {
		if got := mustVersion(c.a).Less(mustVersion(c.b)); got != c.less {
			t.Errorf("%s < %s: %v", c.a, c.b, got)
		}
	}
}

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
	var old *ClaudeCodeTooOld
	if !errors.As(err, &old) || old.Need.String() != "2.0.0" {
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

func (f fakeProtocol) Since() ClaudeCodeVersion { return mustVersion(f.since) }

// `claude --version` runs once per binary, shared through the cache file by
// other processes, and again when the binary changes or an hour has passed.
// A failure is kept for a minute.
func TestInstalledClaudeCodeCached(t *testing.T) {
	calls, version, fail := 0, "2.1.331", error(nil)
	t.Cleanup(SetClaudeVersionForTests(func() (string, error) { calls++; return version, fail }))
	dir := t.TempDir()
	path, cache := filepath.Join(dir, "claude"), filepath.Join(dir, "claude-version.json")
	if err := os.WriteFile(path, []byte("one"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for range 2 {
		if v, err := InstalledClaudeCode(ctx, path, cache); err != nil || v.String() != "2.1.331" {
			t.Fatalf("%v, %v", v, err)
		}
	}
	forget := func() {
		claudeVersions.Lock()
		claudeVersions.m = map[string]cachedVersion{}
		claudeVersions.Unlock()
	}
	forget() // another process: only the file
	if v, _ := InstalledClaudeCode(ctx, path, cache); v.String() != "2.1.331" || calls != 1 {
		t.Fatalf("ran claude --version %d times", calls)
	}
	version = "2.2.0"
	if err := os.WriteFile(path, []byte("updated"), 0o700); err != nil {
		t.Fatal(err)
	}
	if v, _ := InstalledClaudeCode(ctx, path, cache); v.String() != "2.2.0" || calls != 2 {
		t.Fatalf("after an update: %v (%d runs)", v, calls)
	}
	// An hour later (a shim whose file never changes), it's asked again.
	age := func(d time.Duration) {
		claudeVersions.Lock()
		c := claudeVersions.m[path]
		c.At = c.At.Add(-d)
		claudeVersions.m[path] = c
		claudeVersions.Unlock()
	}
	version = "2.3.0"
	age(claudeVersionTTL)
	if v, _ := InstalledClaudeCode(ctx, path, cache); v.String() != "2.3.0" || calls != 3 {
		t.Fatalf("after an hour: %v (%d runs)", v, calls)
	}
	// A failure is kept for a minute, then asked again.
	fail = errors.New("hung")
	age(claudeVersionTTL)
	for range 2 {
		if _, err := InstalledClaudeCode(ctx, path, cache); err == nil {
			t.Fatal("a failing --version gave a version")
		}
	}
	if calls != 4 {
		t.Fatalf("a failure wasn't kept: %d runs", calls)
	}
	fail = nil
	age(claudeVersionErrorTTL)
	if v, err := InstalledClaudeCode(ctx, path, cache); err != nil || v.String() != "2.3.0" || calls != 5 {
		t.Fatalf("after a minute: %v %v (%d runs)", v, err, calls)
	}
}

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
