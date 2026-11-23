package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/firebase/genkit/go/ai"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestClaudeModel(t *testing.T) {
	for in, want := range map[string]string{"": "", "anthropic/claude-haiku-5-5": "claude-haiku-5-5", "claude-opus-5-5": "claude-opus-5-5"} {
		if got, err := ClaudeModel(in); err != nil || got != want {
			t.Errorf("ClaudeModel(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := ClaudeModel("googleai/gemini-flash"); err == nil {
		t.Error("non-Claude model should be rejected")
	}
}

func TestEnvDisablesMemoryAndAPIKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-x")
	t.Setenv("CLAUDE_CODE_DISABLE_AUTO_MEMORY", "0")
	env := strings.Join((&ClaudeCode{}).env(), "\n")
	for _, want := range []string{"CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "CLAUDE_CODE_DISABLE_ORG_MEMORY=1"} {
		if !strings.Contains(env, want) {
			t.Errorf("env lacks %s", want)
		}
	}
	if strings.Contains(env, "ANTHROPIC_API_KEY") || strings.Contains(env, "CLAUDE_CODE_DISABLE_AUTO_MEMORY=0") {
		t.Error("env leaks the API key or a memory override")
	}
}

func TestSplitMessages(t *testing.T) {
	sys, usr, err := splitMessages([]*ai.Message{ai.NewSystemTextMessage("S"), ai.NewUserTextMessage("U")})
	if err != nil || sys != "S" || usr != "U" {
		t.Fatalf("got %q %q %v", sys, usr, err)
	}
	if _, usr, _ := splitMessages([]*ai.Message{ai.NewSystemTextMessage("S")}); usr != startMessage {
		t.Fatalf("system-only prompt should get a start message, got %q", usr)
	}
	if _, _, err := splitMessages([]*ai.Message{ai.NewModelTextMessage("M")}); err == nil {
		t.Fatal("model turns should be rejected")
	}
}

func TestServeToolsRequiresToken(t *testing.T) {
	url, _, stop, err := serveTools(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	ctx := context.Background()
	c := sdk.NewClient(&sdk.Implementation{Name: "t"}, nil)
	if _, err := c.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: url}, nil); err == nil {
		t.Fatal("connect without token should fail")
	}
}

// TestClaudeCodeLive runs a real headless Claude Code session (uses the
// subscription). Opt in with PROCAUTO_LIVE_CLAUDE=1.
func TestClaudeCodeLive(t *testing.T) {
	if os.Getenv("PROCAUTO_LIVE_CLAUDE") == "" {
		t.Skip("set PROCAUTO_LIVE_CLAUDE=1")
	}
	path, err := FindClaude()
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	tool := Tool{
		Name: "secret_word", Description: "Returns the secret word.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		Call: func(_ context.Context, _ map[string]any) (*sdk.CallToolResult, error) {
			calls = append(calls, "secret_word")
			return textResult("pineapple", false), nil
		},
	}
	src := "---\nmodel: anthropic/claude-haiku-5-5\n---\nToday is {{today}}. {{> memories}} Call secret_word and reply with only the word it returns."
	res := &Result{}
	out, err := (&CLI{Tool: &ClaudeCode{Path: path}}).Run(context.Background(), Job{
		Name: "live", Source: src, Input: map[string]any{"today": "Wednesday"},
		Partials: map[string]string{"memories": "Be brief."}, Tools: []Tool{tool, outcomeTool(res)}, MaxTurns: 5,
		Instructions: outcomeInstructions, Effort: "low",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || !strings.Contains(strings.ToLower(out.Text), "pineapple") {
		t.Fatalf("calls=%v text=%q", calls, out.Text)
	}
	if err := verdict(res); err != nil || res.Outcome != OutcomeDone {
		t.Fatalf("the model didn't report done: %v (reported %+v)", err, res.reported)
	}
	t.Logf("text=%q in=%d out=%d cost=$%.4f", out.Text, out.InputTokens, out.OutputTokens, out.CostUSD)
}

func argValue(args []string, flag string) (string, bool) {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

func hasArg(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func TestClaudeCodeEnvironment(t *testing.T) {
	folder := t.TempDir()
	_ = os.WriteFile(filepath.Join(folder, "CLAUDE.md"), []byte("Use the staging API.\n"), 0o600)
	cc := &ClaudeCode{Path: "claude"}
	base := CLIRun{System: "sys", MCP: MCPEndpoint{Name: "procauto", URL: "http://x", Token: "t"}, MaxTurns: 3}

	r := base
	r.Folder, r.SaveSession, r.SessionID, r.BuiltinTools = folder, true, "6f1c2d6e-0000-4000-8000-000000000001", []string{"Read", "Bash"}
	cmd, err := cc.Command(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := argValue(cmd.Args, "--tools"); v != "Read,Bash" {
		t.Errorf("--tools %q", v)
	}
	if v, _ := argValue(cmd.Args, "--allowedTools"); v != "mcp__procauto,Read,Bash" {
		t.Errorf("--allowedTools %q", v)
	}
	if v, _ := argValue(cmd.Args, "--session-id"); v != r.SessionID || hasArg(cmd.Args, "--no-session-persistence") {
		t.Errorf("saved session: %v", cmd.Args)
	}
	if sys, _ := argValue(cmd.Args, "--system-prompt"); !strings.Contains(sys, "Use the staging API.") || !strings.HasPrefix(sys, "sys") {
		t.Errorf("CLAUDE.md not added: %q", sys)
	}
	// One call to the app's tools may take as long as the routine allows,
	// not Claude Code's own minute.
	var mcpConfig struct {
		MCPServers map[string]struct {
			Timeout int64 `json:"timeout"`
		} `json:"mcpServers"`
	}
	if v, _ := argValue(cmd.Args, "--mcp-config"); json.Unmarshal([]byte(v), &mcpConfig) != nil ||
		mcpConfig.MCPServers["procauto"].Timeout != time.Hour.Milliseconds() {
		t.Errorf("--mcp-config timeout: %q", v)
	}
	if v, _ := argValue(cmd.Args, "--setting-sources"); v != "" || !hasArg(cmd.Args, "--restricted") {
		t.Errorf("isolation flags: %v", cmd.Args)
	}

	// Dry run: only read-only built-ins; no session kept unless asked.
	r = base
	r.BuiltinTools, r.DryRun = []string{"Read", "Bash", "Edit", "Grep"}, true
	cmd, _ = cc.Command(context.Background(), r)
	if v, _ := argValue(cmd.Args, "--tools"); v != "Read,Grep" {
		t.Errorf("dry-run --tools %q", v)
	}
	if !hasArg(cmd.Args, "--no-session-persistence") || hasArg(cmd.Args, "--session-id") {
		t.Errorf("unsaved session: %v", cmd.Args)
	}
	if v, ok := argValue(cmd.Args, "--tools"); ok && v != "Read,Grep" {
		t.Errorf("tools %q", v)
	}

	r = base
	r.BuiltinTools = []string{"Bash(rm -rf *)"}
	if _, err := cc.Command(context.Background(), r); err == nil {
		t.Error("a tool pattern was accepted as a built-in name")
	}
}

func TestGenkitRejectsEnvironment(t *testing.T) {
	_, err := (&Genkit{}).Run(context.Background(), Job{Folder: "/tmp"})
	if err == nil || !strings.Contains(err.Error(), "agent CLI backend") {
		t.Fatalf("got %v", err)
	}
}
