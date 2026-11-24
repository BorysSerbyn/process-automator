package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/firebase/genkit/go/genkit"
)

// codexConfig is the --config values of a codex command line, by key.
func codexConfig(args []string) map[string]string {
	out := map[string]string{}
	for i, a := range args {
		if a == "--config" && i+1 < len(args) {
			k, v, _ := strings.Cut(args[i+1], "=")
			out[k] = v
		}
	}
	return out
}

var codexBase = CLIRun{System: "sys", MCP: MCPEndpoint{Name: "procauto", URL: "http://127.0.0.1:5555/mcp", Token: "secret-token"}, MaxTurns: 30}

// A normal run: exactly this command line, the token only in the environment.
func TestCodexCommandLine(t *testing.T) {
	t.Setenv("CODEX_API_KEY", "sk-x")
	t.Setenv("OPENAI_API_KEY", "sk-y")
	r := codexBase
	r.Model, r.Effort = "openai/gpt-5.5", "high"
	cmd, err := (&Codex{Path: "/bin/codex"}).Command(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/bin/codex", "exec", "--json", "--ignore-user-config", "--ignore-rules", "--skip-git-repo-check",
		"--sandbox", "read-only", "--ephemeral", "--model", "gpt-5.5",
		"--config", `approval_policy="never"`,
		"--config", `developer_instructions="sys"`,
		"--config", "project_doc_max_bytes=0",
		"--config", `history.persistence="none"`,
		"--config", "check_for_update_on_startup=false",
		"--config", "analytics.enabled=false",
		"--config", `web_search="disabled"`,
		"--config", "features.shell_tool=false",
		"--config", "features.view_image=false",
		"--config", "features.apps=false", "--config", "features.plugins=false", "--config", "features.tool_suggest=false",
		"--config", "features.multi_agent=false", "--config", "features.image_generation=false", "--config", "features.browser_use=false",
		"--config", "features.computer_use=false", "--config", "features.in_app_browser=false", "--config", "features.memories=false",
		"--config", "features.goals=false", "--config", "features.hooks=false",
		"--config", "skills.include_instructions=false", "--config", "skills.bundled.enabled=false",
		"--config", "tools.experimental_request_user_input.enabled=false",
		"--config", `mcp_servers.procauto.url="http://127.0.0.1:5555/mcp"`,
		"--config", `mcp_servers.procauto.bearer_token_env_var="AUTOMATOR_MCP_TOKEN"`,
		"--config", "mcp_servers.procauto.required=true",
		"--config", `mcp_servers.procauto.default_tools_approval_mode="approve"`,
		"--config", `mcp_servers.procauto.omit_tools_from=["deferred"]`,
		"--config", "mcp_servers.procauto.tool_timeout_sec=3600.0",
		"--config", `model_reasoning_effort="high"`,
		"-",
	}
	if !slices.Equal(cmd.Args, want) {
		t.Errorf("command line\n got %q\nwant %q", cmd.Args, want)
	}
	env := strings.Join(cmd.Env, "\n")
	if !strings.Contains(env, "AUTOMATOR_MCP_TOKEN=secret-token") || strings.Contains(env, "CODEX_API_KEY") || strings.Contains(env, "OPENAI_API_KEY") {
		t.Errorf("env: token missing, or an API key leaked")
	}
	if strings.Contains(strings.Join(cmd.Args, " "), "secret-token") {
		t.Error("the token is on the command line")
	}
}

// A dry run never gets what can change something: a read-only sandbox, no
// network; a saved session isn't ephemeral and the folder's AGENTS.md is in
// the instructions.
func TestCodexEnvironment(t *testing.T) {
	c := &Codex{Path: "codex"}
	all := []string{"shell", "write", "network", "web_search", "view_image"}

	r := codexBase
	r.BuiltinTools = all
	cmd, err := c.Command(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	cfg := codexConfig(cmd.Args)
	if v, _ := argValue(cmd.Args, "--sandbox"); v != "workspace-write" || cfg["sandbox_workspace_write.network_access"] != "true" ||
		cfg["features.shell_tool"] != "true" || cfg["features.view_image"] != "true" || cfg["web_search"] != `"live"` {
		t.Errorf("every built-in allowed: %q", cmd.Args)
	}

	r.DryRun = true
	cmd, _ = c.Command(context.Background(), r)
	cfg = codexConfig(cmd.Args)
	if v, _ := argValue(cmd.Args, "--sandbox"); v != "read-only" {
		t.Errorf("dry run sandbox %q", v)
	}
	if _, ok := cfg["sandbox_workspace_write.network_access"]; ok {
		t.Error("a dry run got network access")
	}
	if cfg["features.shell_tool"] != "true" || cfg["web_search"] != `"live"` {
		t.Errorf("a dry run keeps the read-only built-ins: %v", cfg)
	}

	folder := t.TempDir()
	_ = os.WriteFile(filepath.Join(folder, "AGENTS.md"), []byte("Use the staging API.\n"), 0o600)
	r = codexBase
	r.Folder, r.SaveSession = folder, true
	cmd, _ = c.Command(context.Background(), r)
	if hasArg(cmd.Args, "--ephemeral") {
		t.Error("a saved session is ephemeral")
	}
	var instructions string
	if err := json.Unmarshal([]byte(codexConfig(cmd.Args)["developer_instructions"]), &instructions); err != nil ||
		!strings.HasPrefix(instructions, "sys") || !strings.Contains(instructions, "Use the staging API.") {
		t.Errorf("AGENTS.md not added: %q %v", instructions, err)
	}
}

func TestCodexOutputSchema(t *testing.T) {
	r := codexBase
	r.TempDir, r.OutputSchema = t.TempDir(), fruitSchema
	cmd, err := (&Codex{Path: "codex"}).Command(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	path, ok := argValue(cmd.Args, "--output-schema")
	if !ok || filepath.Dir(path) != r.TempDir {
		t.Fatalf("--output-schema %q, want a file in the run's temp folder", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	_ = json.Unmarshal(b, &s)
	if s["additionalProperties"] != false || len(stringList(s["required"])) != len(s["properties"].(map[string]any)) {
		t.Fatalf("not strict: %s", b)
	}
}

// strictSchema makes every property required and closes every object; a
// property that wasn't required may be null, and that null is dropped again.
func TestStrictSchema(t *testing.T) {
	in := map[string]any{
		"type": "object", "required": []any{"fruit"},
		"properties": map[string]any{
			"fruit": map[string]any{"type": "string"},
			"note":  map[string]any{"type": "string"},
			"box": map[string]any{"type": "object", "properties": map[string]any{
				"size": map[string]any{"type": "integer"},
			}},
			"tags": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
				"name": map[string]any{"type": "string"},
			}}},
		},
	}
	s := strictSchema(in)
	if got := stringList(s["required"]); !slices.Equal(got, []string{"box", "fruit", "note", "tags"}) || s["additionalProperties"] != false {
		t.Fatalf("top level: %v", s)
	}
	props := s["properties"].(map[string]any)
	if _, ok := props["note"].(map[string]any)["anyOf"]; !ok {
		t.Errorf("an optional property isn't nullable: %v", props["note"])
	}
	box := props["box"].(map[string]any)["anyOf"].([]any)[0].(map[string]any)
	if box["additionalProperties"] != false || !slices.Equal(stringList(box["required"]), []string{"size"}) {
		t.Errorf("nested object: %v", box)
	}
	items := props["tags"].(map[string]any)["anyOf"].([]any)[0].(map[string]any)["items"].(map[string]any)
	if items["additionalProperties"] != false {
		t.Errorf("array items: %v", items)
	}
	if _, ok := in["additionalProperties"]; ok {
		t.Error("the routine's schema was changed")
	}

	v := dropOptionalNulls(in, map[string]any{"fruit": "fig", "note": nil, "box": map[string]any{"size": nil}})
	if m := v.(map[string]any); len(m) != 2 || len(m["box"].(map[string]any)) != 0 {
		t.Errorf("nulls kept: %v", v)
	}
	if err := checkOutput(fruitSchema, dropOptionalNulls(fruitSchema, map[string]any{"fruit": "fig", "letters": 3})); err != nil {
		t.Errorf("a matching result fails: %v", err)
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "codex", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCodexResult(t *testing.T) {
	c := &Codex{}
	saved := CLIRun{SaveSession: true}

	out, err := c.Result(saved, readFixture(t, "success.jsonl"), nil, nil)
	if err != nil || out.Text != "pineapple" || out.SessionID != "019a7c3e-5d1b-7f20-9c11-3a5e2b8d4f60" {
		t.Fatalf("success: %+v %v", out, err)
	}
	if out.InputTokens != 12034 || out.CachedTokens != 9984 || out.OutputTokens != 187 {
		t.Errorf("usage: %+v", out)
	}
	if out, _ := c.Result(CLIRun{}, readFixture(t, "success.jsonl"), nil, nil); out.SessionID != "" {
		t.Error("a session that wasn't saved is reported")
	}

	r := CLIRun{OutputSchema: map[string]any{"type": "object", "required": []any{"fruit", "letters"}, "properties": map[string]any{
		"fruit": map[string]any{"type": "string"}, "letters": map[string]any{"type": "integer"}, "note": map[string]any{"type": "string"},
	}}}
	out, err = c.Result(r, readFixture(t, "structured.jsonl"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := out.Structured.(map[string]any); m["fruit"] != "fig" || len(m) != 2 {
		t.Errorf("structured: %#v", out.Structured)
	}
	if _, err := c.Result(r, readFixture(t, "success.jsonl"), nil, nil); err == nil || !strings.Contains(err.Error(), "isn't the JSON") {
		t.Errorf("non-JSON result with a schema: %v", err)
	}

	// Errors that were retried don't fail a turn that completed.
	if out, err := c.Result(CLIRun{}, readFixture(t, "retried.jsonl"), nil, nil); err != nil || out.Text != "Done: nothing new today." {
		t.Errorf("retried: %+v %v", out, err)
	}

	exit := exec.Command("false").Run()
	out, err = c.Result(saved, readFixture(t, "turn-failed.jsonl"), nil, exit)
	if err == nil || !strings.Contains(err.Error(), "usage limit is reached") || !strings.Contains(err.Error(), "3:05 PM") {
		t.Errorf("usage limit: %v", err)
	}
	if out == nil || out.SessionID == "" {
		t.Error("a failed run's saved session isn't reported")
	}

	if _, err := c.Result(CLIRun{}, readFixture(t, "no-final.jsonl"), nil, nil); err == nil || !strings.Contains(err.Error(), "without a final message") {
		t.Errorf("no final message: %v", err)
	}

	// As Codex 0.171.0 printed it when the app's MCP server refused the token.
	stderr := readFixture(t, "mcp-unauthorized.stderr")
	err = func() error { _, err := c.Result(CLIRun{}, nil, stderr, exit); return err }()
	if err == nil || !strings.HasPrefix(err.Error(), "codex: the routine's tools couldn't be reached: thread/start: thread/start failed") ||
		strings.Contains(err.Error(), "codex login") || len(err.Error()) > 700 {
		t.Errorf("MCP server refused: %v", err)
	}
	if _, err := c.Result(CLIRun{}, nil, []byte("Reading prompt from stdin...\nsomething broke\n"), exit); err == nil || !strings.Contains(err.Error(), "something broke") {
		t.Errorf("non-zero exit, plain stderr: %v", err)
	}
	if _, err := c.Result(CLIRun{}, readFixture(t, "success.jsonl"), nil, exit); err == nil {
		t.Error("a non-zero exit passed")
	}
	if _, err := c.Result(CLIRun{}, []byte(`{"type":"thread.started","thread_id":"x"}`+"\n"), nil, nil); err == nil ||
		!strings.Contains(err.Error(), "before its turn completed") {
		t.Errorf("cut off: %v", err)
	}
	if err := codexError("unexpected status 401 Unauthorized"); !strings.Contains(err.Error(), "codex login") {
		t.Errorf("sign-in: %v", err)
	}
}

func TestCodexModelAndEffort(t *testing.T) {
	for in, want := range map[string]string{"": "", "openai/gpt-5.5": "gpt-5.5", "gpt-5.5-codex": "gpt-5.5-codex"} {
		if got, err := CodexModel(in); err != nil || got != want {
			t.Errorf("CodexModel(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := CodexModel("anthropic/claude-haiku-5-5"); err == nil || !strings.Contains(err.Error(), "only OpenAI models") {
		t.Errorf("a Claude model: %v", err)
	}
	for _, e := range []string{"", "low", "medium", "high", "xhigh", "max"} {
		if got, err := codexEffort(NewestCodex(), e); err != nil || got != e {
			t.Errorf("effort %q: %q %v", e, got, err)
		}
	}
	if _, err := codexEffort(NewestCodex(), "turbo"); err == nil {
		t.Error("an unknown effort was accepted")
	}
	r := codexBase
	cmd, _ := (&Codex{Path: "codex"}).Command(context.Background(), r)
	if hasArg(cmd.Args, "--model") {
		t.Error("no model should leave it to Codex")
	}
	if _, ok := codexConfig(cmd.Args)["model_reasoning_effort"]; ok {
		t.Error("no effort should leave it to the model")
	}
	r.Model = "anthropic/claude-haiku-5-5"
	if _, err := (&Codex{Path: "codex"}).Command(context.Background(), r); err == nil {
		t.Error("a Claude model ran on codex")
	}
}

func TestCodexBuiltins(t *testing.T) {
	if err := CheckCodexBuiltins([]string{"shell", "web_search"}); err != nil {
		t.Error(err)
	}
	for _, bad := range [][]string{{"Bash"}, {"Read"}, {"network"}} {
		if err := CheckCodexBuiltins(bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	got, _ := NewestCodex().AllowBuiltins([]string{"shell", "write", "network", "view_image"}, true)
	if len(got) != 2 || !got["shell"] || !got["view_image"] {
		t.Errorf("dry run: %v", got)
	}
	if err := CheckClaudeBuiltins([]string{"shell"}); err == nil {
		t.Error("claude-code took a Codex name")
	}
}

func TestTOMLString(t *testing.T) {
	for in, want := range map[string]string{
		`plain`:              `"plain"`,
		"a \"q\" \\ <b>\n\t": `"a \"q\" \\ <b>\n\t"`,
		"del\x7f":            `"del\u007f"`,
		"é ✓":                `"é ✓"`,
	} {
		if got := tomlString(in); got != want {
			t.Errorf("tomlString(%q) = %s, want %s", in, got, want)
		}
	}
}

// The whole CLI path with a stand-in codex: the prompt on stdin, the token in
// the environment, Codex's session ID reported only when it's kept.
func TestCodexThroughCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as codex")
	}
	script := filepath.Join(t.TempDir(), "codex")
	body := "#!/bin/sh\n" +
		`[ -n "$AUTOMATOR_MCP_TOKEN" ] || { echo "no token" >&2; exit 1; }` + "\n" +
		`prompt=$(cat)` + "\n" +
		`echo '{"type":"thread.started","thread_id":"t-123"}'` + "\n" +
		`printf '{"type":"item.completed","item":{"id":"i","type":"agent_message","text":"%s"}}\n' "$prompt"` + "\n" +
		`echo '{"type":"turn.completed","usage":{"input_tokens":1,"cached_input_tokens":0,"output_tokens":1}}'` + "\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	b := &CLI{Tool: &Codex{Path: script}}
	if !PicksSessionID(b) || PicksSessionID(&CLI{Tool: &ClaudeCode{}}) {
		t.Fatal("codex picks its session IDs; claude-code takes the caller's")
	}
	src := "---\nmodel: openai/gpt-5.5\n---\nSay hi."
	out, err := b.Run(context.Background(), Job{Name: "t", Source: src, SaveSession: true})
	if err != nil || out.Text != "Say hi." || out.SessionID != "t-123" {
		t.Fatalf("saved: %+v %v", out, err)
	}
	if out, err := b.Run(context.Background(), Job{Name: "t", Source: src}); err != nil || out.SessionID != "" {
		t.Fatalf("not saved: %+v %v", out, err)
	}
	var exit *exec.ExitError
	if _, err := b.Run(context.Background(), Job{Name: "t", Source: "---\nmodel: googleai/gemini\n---\nx"}); err == nil || errors.As(err, &exit) {
		t.Fatalf("a non-OpenAI model: %v", err)
	}
}

// Picoschema's optional fields (a type beside anyOf [that type, null], seen
// sent to Codex as is) become just the alternatives in the strict form; a
// null the model gives for one is dropped, and the result checks against the
// routine's own schema.
func TestStrictSchemaFromPicoschema(t *testing.T) {
	g := genkit.Init(context.Background())
	src := "---\noutput:\n  schema:\n    fruit: string\n    note?: string\n    box?(object):\n      size: integer\n---\nName a fruit."
	p, err := genkit.LoadPromptFromSource(g, src, "f", "")
	if err != nil {
		t.Fatal(err)
	}
	opts, err := p.Render(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	schema := outputSchema(opts)
	props := strictSchema(schema)["properties"].(map[string]any)
	note := props["note"].(map[string]any)
	if _, typed := note["type"]; typed || len(note["anyOf"].([]any)) != 2 || !nullable(note) {
		t.Errorf("note: %v", note)
	}
	box := props["box"].(map[string]any)
	inner := box["anyOf"].([]any)[0].(map[string]any)
	if _, typed := box["type"]; typed || inner["additionalProperties"] != false || !slices.Equal(stringList(inner["required"]), []string{"size"}) {
		t.Errorf("box: %v", box)
	}
	v := dropOptionalNulls(schema, map[string]any{"fruit": "fig", "note": nil, "box": nil})
	if err := checkOutput(schema, v); err != nil {
		t.Errorf("a strict result fails the routine's schema: %v (%v)", err, v)
	}
}

// Events as Codex 0.171.0 printed them for a saved-session run (with a fake
// model provider): the thread ID, tool calls, final message and usage.
func TestCodexResultRecorded(t *testing.T) {
	out, err := (&Codex{}).Result(CLIRun{SaveSession: true}, readFixture(t, "recorded-0.171.0.jsonl"), nil, nil)
	if err != nil || out.Text != "pineapple" || out.SessionID != "01a1ff87-7bc8-7563-ac62-d49ae02c5096" ||
		out.InputTokens != 3003 || out.CachedTokens != 1800 || out.OutputTokens != 153 {
		t.Fatalf("%+v %v", out, err)
	}
}
