package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/engine/enginetest"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/secrets"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// The test binary doubles as the procauto CLI, so the MCP server's tools
// (which run the CLI as a subprocess) can be tested end to end.
const cliEnv = "PROCAUTO_TEST_AS_CLI"

// testSecrets replaces the keychain in every test (and in the test binary
// run as the CLI), so tests never touch real credentials.
var testSecrets = enginetest.Secrets

// loadTest loads the app as commands do, with plugins and the test secrets.
func loadTest(cfg string, plugins []pluginapi.Plugin) (*engine.App, error) {
	return engine.Load(cfg, plugins, testSecrets)
}

func TestMain(m *testing.M) {
	secretStore = func() secrets.Store { return testSecrets }
	useLoginPath = false // tests never run the login shell
	// Tests never touch Claude Code's real settings (the status line, its MCP servers).
	claudeDir, err := os.MkdirTemp("", "pt-claude-")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	// Nor run the real claude (mcp connect).
	runClaudeMCP = fakeClaudeMCP
	// Tests never open apps, files or browsers on this machine.
	openExternal = func(string) error { return errors.New("tests don't open things") }
	// Nor run the real gh: the release token is GH_TOKEN's, if a test sets it.
	engine.FindReleaseToken = func(context.Context, string, string) (string, []string) { return os.Getenv("GH_TOKEN"), nil }
	// Nor reach GitHub: tests that read releases serve their own.
	product.ReleaseWeb, product.ReleaseAPI = "http://127.0.0.1:1", "http://127.0.0.1:1"
	if mode := os.Getenv(pluginEnv); mode != "" {
		servePluginFixture(mode)
		os.Exit(0)
	}
	if os.Getenv(cliEnv) == "1" {
		root := NewRoot(nil)
		root.SetArgs(os.Args[1:])
		if err := root.Execute(); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	code := m.Run()
	_ = os.RemoveAll(claudeDir)
	os.Exit(code)
}

func testServer(t *testing.T, readOnly bool) *automatorServer {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return &automatorServer{
		root: NewRoot(nil), exe: exe, env: []string{cliEnv + "=1"},
		configPath: filepath.Join(t.TempDir(), "config.yaml"), readOnly: readOnly,
	}
}

func toolMap(s *automatorServer) map[string]cliTool {
	m := map[string]cliTool{}
	for _, t := range s.tools() {
		m[t.tool.Name] = t
	}
	return m
}

func TestOverridesNameCommands(t *testing.T) {
	root := NewRoot(nil)
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	for key := range mcpOverrides() {
		c, _, err := root.Find(strings.Fields(key))
		if err != nil || commandKey(c) != key {
			t.Errorf("override %q names no command", key)
		}
	}
}

// Every command is a tool unless an override excludes it; safety comes from
// the command's own annotation, defaulting to destructive.
func TestToolsDerivedFromCommands(t *testing.T) {
	tools := toolMap(testServer(t, false))
	for _, name := range []string{"routines_list", "routines_create", "settings_set", "plugins_add", "auth_login", "run", "run_dry_run", "update", "update_check", "mcp_call"} {
		if _, ok := tools[name]; !ok {
			t.Errorf("missing tool %s", name)
		}
	}
	for _, name := range []string{"daemon", "mcp_serve", "help", "completion_zsh"} {
		if _, ok := tools[name]; ok {
			t.Errorf("excluded command served: %s", name)
		}
	}
	for name, wantRO := range map[string]bool{"routines_list": true, "runs_show": true, "run": false, "run_dry_run": true, "routines_delete": false, "update_check": true} {
		a := tools[name].tool.Annotations
		if a.ReadOnlyHint != wantRO || (!wantRO && (a.DestructiveHint == nil || !*a.DestructiveHint)) {
			t.Errorf("%s: annotations %+v, want read-only %v", name, a, wantRO)
		}
	}
	props := tools["run"].tool.InputSchema.(map[string]any)["properties"].(map[string]any)
	if _, ok := props["dry_run"]; ok {
		t.Error("run (the real thing) shouldn't take dry_run; run_dry_run is the read-only variant")
	}
	if _, ok := props["routine"]; !ok {
		t.Errorf("run's positional argument missing: %v", props)
	}
	if req := tools["run"].tool.InputSchema.(map[string]any)["required"]; req == nil {
		t.Error("run's routine argument isn't required")
	}
}

// --read-only serves only read-only tools, minus those that run anything.
func TestReadOnlyServer(t *testing.T) {
	tools := toolMap(testServer(t, true))
	if _, ok := tools["routines_list"]; !ok {
		t.Error("routines_list missing")
	}
	for name, tool := range tools {
		if !tool.ro {
			t.Errorf("%s isn't read-only", name)
		}
	}
	for _, name := range []string{"run", "run_dry_run", "auth_login", "routines_create"} {
		if _, ok := tools[name]; ok {
			t.Errorf("%s served read-only", name)
		}
	}
}

func TestArgv(t *testing.T) {
	tools := toolMap(testServer(t, false))
	got, _, err := tools["templates_install"].argv("/c.yaml", map[string]any{
		"template": "-weird", "as": "x", "set": []any{"a=1", "b=2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "--config /c.yaml templates install --json --as=x --set=a=1 --set=b=2 -- -weird"
	if strings.Join(got, " ") != want {
		t.Fatalf("argv\n got %s\nwant %s", strings.Join(got, " "), want)
	}
	if _, _, err := tools["run"].argv("/c.yaml", map[string]any{}); err == nil {
		t.Error("missing required argument accepted")
	}
}

// End to end through an MCP client: create a routine, see it listed disabled,
// and find the change logged.
func TestServeEndToEnd(t *testing.T) {
	s := testServer(t, false)
	withNotesServer(t, s.configPath)
	srv, err := s.build()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ct, st := sdk.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "test"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	call := func(name string, args map[string]any) *sdk.CallToolResult {
		t.Helper()
		res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	src := "---\nautomator.servers: [notes]\nautomator.schedule: \"0 9 * * 1-5\"\n---\nSay hi. {{> memories}}\n"
	if res := call("routines_create", map[string]any{"routine": "hello", "source": src}); res.IsError {
		t.Fatalf("create: %s", resultText(res))
	}
	if res := call("routines_create", map[string]any{"routine": "hello", "source": src}); !res.IsError {
		t.Fatal("create overwrote")
	}
	res := call("routines_list", nil)
	var rows []RoutineInfo
	if err := json.Unmarshal([]byte(res.Content[0].(*sdk.TextContent).Text), &rows); err != nil {
		t.Fatalf("list: %v: %s", err, resultText(res))
	}
	if len(rows) != 1 || rows[0].Name != "hello" || rows[0].Enabled {
		t.Fatalf("created routine should be listed, disabled: %+v", rows)
	}
	log, err := os.ReadFile(engine.LayoutOf(s.configPath).MCPChanges())
	if err != nil || strings.Count(string(log), "\n") != 2 || !strings.Contains(string(log), `"tool":"routines_create"`) {
		t.Fatalf("change log (2 creates, no reads): %s %v", log, err)
	}
}

func resultText(res *sdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			b.WriteString(tc.Text + "\n")
		}
	}
	return b.String()
}

func TestToolSchemaDetails(t *testing.T) {
	tools := toolMap(testServer(t, false))
	for _, name := range []string{"settings_list", "settings_get", "settings_set", "routines_list"} {
		if !tools[name].jsonOut {
			t.Errorf("%s doesn't ask its command for --json", name)
		}
	}
	props := tools["routines_create"].tool.InputSchema.(map[string]any)["properties"].(map[string]any)
	if _, ok := props["from"]; ok {
		t.Error("routines_create exposes --from (reads any local file) over MCP")
	}
	if _, ok := props["source"]; !ok {
		t.Error("routines_create lacks source")
	}
	login := tools["auth_login"].tool.InputSchema.(map[string]any)["properties"].(map[string]any)
	if _, ok := login["browser"]; ok || len(login) != 1 {
		t.Errorf("auth_login should take only its server argument: %v", login)
	}
	if _, _, err := tools["run"].argv("/c.yaml", map[string]any{"routine": nil}); err == nil {
		t.Error("a null required argument was accepted")
	}
	argv, stdin, _ := tools["routines_create"].argv("/c.yaml", map[string]any{"routine": "x", "source": "big"})
	if stdin != "big" || !strings.Contains(strings.Join(argv, " "), "--from=-") || strings.Contains(strings.Join(argv, " "), "big") {
		t.Errorf("source should go on stdin: %v / %q", argv, stdin)
	}
	create := tools["routines_create"]
	for args, ok := range map[string]bool{
		`{"routine":"x","source":"s"}`:        true,
		`{"routine":123}`:                     false,
		`{"routine":"x","from":"/etc/hosts"}`: false,
		`{"routine":"x","nope":1}`:            false,
	} {
		var m map[string]any
		_ = json.Unmarshal([]byte(args), &m)
		if err := create.checkArgs(m); (err == nil) != ok {
			t.Errorf("checkArgs(%s) = %v", args, err)
		}
	}
	if err := tools["runs"].checkArgs(map[string]any{"limit": 1.5}); err == nil {
		t.Error("non-integer limit accepted")
	}
}

// Sign-in through a client that shows URLs: the client opens the page and
// the key reaches the secret store, never the conversation. Both protocol
// generations: input requests and retries (current), and server-initiated
// elicitation (older, done by the SDK's middleware).
func TestMCPLogin(t *testing.T) {
	for _, version := range []string{"", "2025-11-25"} {
		for _, decline := range []bool{false, true} {
			name := fmt.Sprintf("protocol %q decline %v", version, decline)
			_ = testSecrets.Delete(engine.AnthropicKey)
			s := testServer(t, false)
			srv, err := s.build()
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			ct, st := sdk.NewInMemoryTransports()
			if _, err := srv.Connect(ctx, st, nil); err != nil {
				t.Fatal(err)
			}
			shown := 0
			client := sdk.NewClient(&sdk.Implementation{Name: "test"}, &sdk.ClientOptions{
				Capabilities: &sdk.ClientCapabilities{Elicitation: &sdk.ElicitationCapabilities{URL: &sdk.URLElicitationCapabilities{}}},
				ElicitationHandler: func(_ context.Context, req *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
					shown++
					if decline {
						return &sdk.ElicitResult{Action: "decline"}, nil
					}
					go func() { _, _ = http.PostForm(req.Params.URL, url.Values{"value": {"sk-test"}}) }()
					return &sdk.ElicitResult{Action: "accept"}, nil
				},
			})
			cs, err := client.Connect(ctx, ct, &sdk.ClientSessionOptions{ProtocolVersion: version})
			if err != nil {
				t.Fatal(err)
			}
			res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "auth_login", Arguments: map[string]any{"server": "anthropic"}})
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			got, _ := testSecrets.Get(engine.AnthropicKey)
			switch {
			case shown != 1:
				t.Errorf("%s: page shown %d times", name, shown)
			case decline && (!res.IsError || got != ""):
				t.Errorf("%s: declining should fail and store nothing: %s", name, resultText(res))
			case !decline && (res.IsError || got != "sk-test"):
				t.Errorf("%s: key not stored (%q): %s", name, got, resultText(res))
			case strings.Contains(resultText(res), "sk-test"):
				t.Errorf("%s: the secret reached the result", name)
			}
			_ = cs.Close()
		}
	}
}

// The MCP server serves the guide as one read-only tool taking a topic, as
// text, and its instructions send clients to it.
func TestGuideOverMCP(t *testing.T) {
	s := testServer(t, true)
	g, ok := toolMap(s)["guide"]
	if !ok {
		t.Fatal("no guide tool")
	}
	if !g.tool.Annotations.ReadOnlyHint || g.jsonOut {
		t.Errorf("guide: read-only %v, --json %v", g.tool.Annotations.ReadOnlyHint, g.jsonOut)
	}
	topic := g.tool.InputSchema.(map[string]any)["properties"].(map[string]any)["topic"].(map[string]any)
	if enum, _ := topic["enum"].([]string); len(enum) != len(guideTopics) {
		t.Errorf("the topic argument doesn't list the topics: %v", topic)
	}
	for _, topic := range guideTopics {
		if !strings.Contains(g.tool.Description, topic.Summary) {
			t.Errorf("the tool's description lacks %s", topic.Name)
		}
	}
	if strings.Contains(mcpInstructions, "settings_set") || strings.Contains(mcpInstructions, ".enabled") ||
		!strings.Contains(mcpInstructions, "guide with topic routines") || !strings.Contains(mcpInstructions, "routines trust") {
		t.Errorf("instructions: %s", mcpInstructions)
	}

	srv, err := s.build()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ct, st := sdk.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "guide", Arguments: map[string]any{"topic": "routines"}})
	if err != nil || res.IsError || !strings.HasPrefix(resultText(res), "# Writing a routine") {
		t.Fatalf("guide over MCP: %v %s", err, resultText(res))
	}
}
