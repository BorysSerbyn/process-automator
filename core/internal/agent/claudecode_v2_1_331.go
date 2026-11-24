package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/statusline"
	"github.com/BorysSerbyn/process-automator/core/internal/usage"
)

// claudeCode2_1_331 is Claude Code from 2.1.331.
type claudeCode2_1_331 struct{}

func (claudeCode2_1_331) Since() Version { return mustVersion("2.1.331") }

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

// claudeBuiltins2_1_331: any of Claude Code's tool names goes; these are the
// common ones, and the read-only ones are all a dry run gets.
var claudeBuiltins2_1_331 = BuiltinSet{Open: true, Tools: []BuiltinTool{
	{Name: "Read", Allows: "reading files", ReadOnly: true},
	{Name: "Glob", Allows: "finding files by name pattern", ReadOnly: true},
	{Name: "Grep", Allows: "searching file contents", ReadOnly: true},
	{Name: "WebSearch", Allows: "searching the web", ReadOnly: true},
	{Name: "WebFetch", Allows: "fetching a web page", ReadOnly: true},
	{Name: "Bash", Allows: "running shell commands"},
	{Name: "Edit", Allows: "changing files"},
	{Name: "Write", Allows: "creating and overwriting files"},
}}

func (claudeCode2_1_331) ProjectInstructions() string { return "CLAUDE.md" }

func (claudeCode2_1_331) Builtins() *BuiltinSet { return &claudeBuiltins2_1_331 }

// MCPServer reads .claude.json, in $CLAUDE_CONFIG_DIR or my home folder:
// its top-level mcpServers are the user-scope ones (what `claude mcp add
// --scope user` registers).
func (claudeCode2_1_331) MCPServer(name string) (ClaudeMCPServer, bool, error) {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ClaudeMCPServer{}, false, err
		}
		dir = home
	}
	b, err := os.ReadFile(filepath.Join(dir, ".claude.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return ClaudeMCPServer{}, false, nil
	}
	if err != nil {
		return ClaudeMCPServer{}, false, err
	}
	var cfg struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return ClaudeMCPServer{}, false, fmt.Errorf("reading Claude Code's config: %w", err)
	}
	s, ok := cfg.MCPServers[name]
	return ClaudeMCPServer{Command: s.Command, Args: s.Args}, ok, nil
}

func (claudeCode2_1_331) PlanLimit(msg string, now time.Time) *PlanLimitError {
	return planLimitError(msg, now)
}

func (claudeCode2_1_331) SignInHint(msg string) string {
	msg = strings.ToLower(msg)
	if !strings.Contains(msg, "log in") && !strings.Contains(msg, "login") {
		return ""
	}
	return " (sign in by running `claude` once, or store a `claude setup-token` token with `" + product.Command + " auth login claude-code`)"
}

// StatusLine: settings.json in $CLAUDE_CONFIG_DIR or ~/.claude, its
// statusLine entry ({"type": "command", "command": …}).
func (claudeCode2_1_331) StatusLine() (statusline.Format, error) {
	path := ""
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		path = filepath.Join(dir, "settings.json")
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return statusline.Format{}, err
		}
		path = filepath.Join(home, ".claude", "settings.json")
	}
	return statusline.Format{SettingsPath: path, Key: "statusLine", Type: "command", TypeField: "type", CommandField: "command"}, nil
}

// StatusLineUsage: rate_limits' five_hour and seven_day, each with
// used_percentage and resets_at (Unix seconds).
func (claudeCode2_1_331) StatusLineUsage(input []byte) (session, weekly *usage.PlanWindow, err error) {
	var in struct {
		RateLimits struct {
			FiveHour *statusWindow `json:"five_hour"`
			SevenDay *statusWindow `json:"seven_day"`
		} `json:"rate_limits"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, nil, fmt.Errorf("status line input: %w", err)
	}
	return in.RateLimits.FiveHour.window(), in.RateLimits.SevenDay.window(), nil
}

type statusWindow struct {
	UsedPercentage *float64 `json:"used_percentage"`
	ResetsAt       *int64   `json:"resets_at"`
}

func (s *statusWindow) window() *usage.PlanWindow {
	if s == nil || s.UsedPercentage == nil || s.ResetsAt == nil {
		return nil
	}
	return &usage.PlanWindow{UsedPercent: *s.UsedPercentage, ResetsAt: time.Unix(*s.ResetsAt, 0)}
}
