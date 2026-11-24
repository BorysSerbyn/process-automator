package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/BorysSerbyn/process-automator/core/internal/usage"
)

// claudeCode2_1_331 is Claude Code from 2.1.331.
type claudeCode2_1_331 struct{}

func (claudeCode2_1_331) Since() ClaudeCodeVersion { return mustVersion("2.1.331") }

func (claudeCode2_1_331) RunArgs(r ClaudeRun) []string {
	mcpConfig, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{
		r.MCP.Name: map[string]any{
			"type": "http", "url": r.MCP.URL,
			"headers": map[string]string{"Authorization": "Bearer " + r.MCP.Token},
			// Claude Code's own limit on one tool call over HTTP (about a
			// minute by default) would cut off a slow plugin call (a first
			// git fetch of the day in a script) long before the routine's
			// timeout: the routine's timeout bounds a run, as codex's
			// tool_timeout_sec does there.
			"timeout": r.ToolTimeout.Milliseconds(),
		},
	}})
	args := []string{
		"-p", "--output-format", "json",
		"--system-prompt", r.System,
		"--tools", strings.Join(r.Builtins, ","), // "" is none
		"--strict-mcp-config", "--mcp-config", string(mcpConfig),
		"--allowedTools", strings.Join(append([]string{"mcp__" + r.MCP.Name}, r.Builtins...), ","),
		"--permission-mode", "dontAsk", // anything not allowed is denied, never prompted
		"--setting-sources", "", // ignore settings, hooks and plugins (user's and the folder's)
		"--restricted", // file tools stay in the working folder
		"--disable-slash-commands",
		"--no-chrome",
		"--max-turns", fmt.Sprint(r.MaxTurns),
	}
	if r.Schema != "" {
		args = append(args, "--json-schema", r.Schema)
	}
	if r.SessionID != "" {
		args = append(args, "--session-id", r.SessionID)
	} else {
		args = append(args, "--no-session-persistence")
	}
	if r.Model != "" {
		args = append(args, "--model", r.Model)
	}
	if r.Effort != "" {
		args = append(args, "--effort", r.Effort)
	}
	return args
}

func (claudeCode2_1_331) RunEnv(base []string, oauthToken string) []string {
	var env []string
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN",
			"CLAUDE_CODE_DISABLE_AUTO_MEMORY", "CLAUDE_CODE_DISABLE_ORG_MEMORY":
			continue
		}
		env = append(env, kv)
	}
	// Memories are user-driven only (`procauto memory`): Claude Code must not
	// write its own, nor read org-level ones into the routine.
	env = append(env, "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "CLAUDE_CODE_DISABLE_ORG_MEMORY=1")
	if oauthToken != "" {
		env = append(env, "CLAUDE_CODE_OAUTH_TOKEN="+oauthToken)
	}
	return env
}

// Result reads `--output-format json`'s result.
func (claudeCode2_1_331) Result(stdout []byte) (ClaudeResult, error) {
	var res struct {
		Result     string `json:"result"`
		Structured any    `json:"structured_output"`
		IsError    bool   `json:"is_error"`
		Subtype    string `json:"subtype"`
		// total_cost_usd is left out on purpose: on a Claude plan it's an
		// estimate at API prices, not a charge, so nothing records it.
		Usage struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(stdout, &res); err != nil {
		return ClaudeResult{}, err
	}
	u := res.Usage
	return ClaudeResult{
		Text: res.Result, Structured: res.Structured, IsError: res.IsError, Subtype: res.Subtype,
		InputTokens:  u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens,
		OutputTokens: u.OutputTokens, CachedTokens: u.CacheReadInputTokens,
	}, nil
}

// UsageArgs: `claude -p /usage` with stream-json output carries the plan's
// usage as a usage_report (documented in the Agent SDK as experimental).
// /usage is a local command: no model call, no tokens. It needs the
// claude.ai login (a setup token gets no report).
func (claudeCode2_1_331) UsageArgs() []string {
	return []string{"-p", "/usage", "--output-format", "stream-json", "--verbose", "--no-session-persistence",
		"--setting-sources", "", "--strict-mcp-config", "--tools", "", "--restricted"}
}

func (claudeCode2_1_331) UsageReport(stdout []byte) ([]usage.Limit, error) {
	return parseUsageReport(stdout)
}

func (claudeCode2_1_331) MCPAddArgs(name string, command []string) []string {
	return append([]string{"mcp", "add", "--scope", "user", name, "--"}, command...)
}

func (claudeCode2_1_331) MCPRemoveArgs(name string) []string {
	return []string{"mcp", "remove", "--scope", "user", name}
}
