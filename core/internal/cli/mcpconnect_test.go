package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/engine/enginetest"
)

// fakeClaudeMCP stands in for `claude mcp add|remove --scope user`: it edits
// .claude.json in $CLAUDE_CONFIG_DIR (a temporary folder in tests) the way
// Claude Code does, keeping what else is there.
func fakeClaude(_ context.Context, _ string, args ...string) error {
	if len(args) == 0 || args[0] != "mcp" {
		return errors.New("not a claude mcp call")
	}
	args = args[1:]
	path := claudeConfig()
	cfg := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &cfg); err != nil {
			return err
		}
	}
	servers, _ := cfg["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	switch {
	case len(args) >= 4 && args[0] == "remove" && args[1] == "--scope" && args[2] == "user":
		delete(servers, args[3])
	case len(args) >= 6 && args[0] == "add" && args[1] == "--scope" && args[2] == "user" && args[4] == "--":
		if _, ok := servers[args[3]]; ok {
			return errors.New("already exists")
		}
		servers[args[3]] = map[string]any{"type": "stdio", "command": args[5], "args": args[6:], "env": map[string]any{}}
	default:
		return errors.New("unexpected claude mcp call")
	}
	cfg["mcpServers"] = servers
	b, _ := json.Marshal(cfg)
	return os.WriteFile(path, b, 0o600)
}

// fakeCodex stands in for `codex mcp add|remove|list --json`: the servers
// are kept as JSON in $CODEX_HOME (a temporary folder in tests).
func fakeCodex(_ context.Context, _ string, args ...string) ([]byte, error) {
	path := filepath.Join(os.Getenv("CODEX_HOME"), "servers.json")
	type server struct {
		Name      string `json:"name"`
		Transport struct {
			Type    string   `json:"type"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"transport"`
	}
	var list []server
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &list)
	}
	save := func() ([]byte, error) {
		b, _ := json.Marshal(list)
		return nil, os.WriteFile(path, b, 0o600)
	}
	switch {
	case slices.Equal(args, []string{"mcp", "list", "--json"}):
		if list == nil {
			return []byte("[]"), nil
		}
		return json.Marshal(list)
	case len(args) == 3 && args[0] == "mcp" && args[1] == "remove":
		list = slices.DeleteFunc(list, func(s server) bool { return s.Name == args[2] })
		return save()
	case len(args) >= 5 && args[0] == "mcp" && args[1] == "add" && args[3] == "--":
		var s server
		s.Name, s.Transport.Type, s.Transport.Command, s.Transport.Args = args[2], "stdio", args[4], args[5:]
		list = append(list, s)
		return save()
	}
	return nil, errors.New("unexpected codex call: " + strings.Join(args, " "))
}

// claudeConfig is Claude Code's config file in the test's CLAUDE_CONFIG_DIR.
func claudeConfig() string { return filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), ".claude.json") }

// connectClaude gives a test its own Claude Code config, with the app's MCP
// server registered.
func connectClaude(t *testing.T) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	if err := fakeClaude(context.Background(), "claude", agent.NewestClaudeCode().MCPAddArgs("procauto", []string{os.Args[0], "mcp", "serve"})...); err != nil {
		t.Fatal(err)
	}
}

// On claude-code, setup requires Claude Code to have the app's MCP server,
// and does it without asking; mcp connect registers it once.
func TestMCPConnect(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	a, _ := enginetest.Fixture(t, nil, nil)
	cfg := a.Layout.Config()
	step := func() (engine.SetupStep, bool) {
		t.Helper()
		out, err := a.Setup()
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range out.Steps {
			if s.Step == engine.StepMCP {
				return s, out.Done
			}
		}
		return engine.SetupStep{}, out.Done
	}
	s, done := step()
	if s.Done || s.Optional || !s.Auto || done || !slices.Contains(s.Commands, "procauto mcp connect") {
		t.Fatalf("not connected: %+v (setup done %v)", s, done)
	}
	// Something else in Claude Code's config is kept.
	_ = os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(`{"mcpServers":{"other":{"command":"x"}},"theme":"dark"}`), 0o600)
	if _, err := cli(t, cfg, "mcp", "connect"); err != nil {
		t.Fatal(err)
	}
	if s, done = step(); !s.Done || !done {
		t.Errorf("connected, yet: %+v (setup done %v)", s, done)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".claude.json"))
	var got struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
		Theme string `json:"theme"`
	}
	_ = json.Unmarshal(b, &got)
	srv := got.MCPServers["procauto"]
	if got.Theme != "dark" || len(got.MCPServers) != 2 || !slices.Equal(srv.Args, []string{"mcp", "serve"}) || !filepath.IsAbs(srv.Command) {
		t.Errorf("Claude Code's config: %s", b)
	}
	// Again: nothing changes.
	if out, err := cli(t, cfg, "mcp", "connect", "--json"); err != nil || !json.Valid([]byte(out)) || !strings.Contains(out, `"already": true`) {
		t.Errorf("second connect: %s %v", out, err)
	}
	// One whose command is gone isn't connected; connect replaces it.
	_ = os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(`{"mcpServers":{"procauto":{"command":"/gone/procauto","args":["mcp","serve"]}}}`), 0o600)
	if s, _ = step(); s.Done {
		t.Errorf("a server whose command is gone counts: %+v", s)
	}
	if _, err := cli(t, cfg, "mcp", "connect"); err != nil {
		t.Fatal(err)
	}
	if s, _ = step(); !s.Done {
		t.Errorf("not replaced: %+v", s)
	}
	// On the claude-api backend there's no such step.
	if _, err := cli(t, cfg, "settings", "set", "llm.backend", "claude-api"); err != nil {
		t.Fatal(err)
	}
	if s, _ = step(); s.Step != "" {
		t.Errorf("a Claude Code step on the claude-api backend: %+v", s)
	}
}

// On codex, setup requires Codex to have the app's MCP server, and mcp
// connect registers it there (and with Claude Code, installed too).
func TestMCPConnectCodex(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	engine.ForgetCodexMCP()
	a, _ := enginetest.Fixture(t, nil, nil)
	cfg := a.Layout.Config()
	if _, err := cli(t, cfg, "settings", "set", "llm.backend", "codex"); err != nil {
		t.Fatal(err)
	}
	step := func() engine.SetupStep {
		t.Helper()
		st, ok, err := a.MCPStep()
		if err != nil || !ok {
			t.Fatalf("no step: %v", err)
		}
		return st
	}
	if s := step(); s.Done || s.Title != "Codex connection" {
		t.Fatalf("not connected: %+v", s)
	}
	out, err := cli(t, cfg, "mcp", "connect", "--json")
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	var got MCPConnectOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got.Clients) != 2 || got.Already {
		t.Fatalf("connect: %s", out)
	}
	engine.ForgetCodexMCP()
	if s := step(); !s.Done {
		t.Fatalf("connected, yet: %+v", s)
	}
	b, _ := os.ReadFile(filepath.Join(os.Getenv("CODEX_HOME"), "servers.json"))
	if !strings.Contains(string(b), `"name":"procauto"`) || !strings.Contains(string(b), `"args":["mcp","serve"]`) {
		t.Fatalf("Codex's servers: %s", b)
	}
	if out, err := cli(t, cfg, "mcp", "connect", "--json"); err != nil || !strings.Contains(out, `"already": true`) {
		t.Fatalf("second connect: %s %v", out, err)
	}
}
