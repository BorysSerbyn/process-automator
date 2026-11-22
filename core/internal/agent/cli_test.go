package agent

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeCLI is an agent CLI that echoes its stdin; Command checks the tool server.
type fakeCLI struct {
	t       *testing.T
	run     CLIRun
	tools   []string
	tokenOK bool
}

func (*fakeCLI) Name() string { return "fake" }

func (f *fakeCLI) Command(ctx context.Context, r CLIRun) (*exec.Cmd, error) {
	f.run = r
	// The tools are reachable with the token, and only with it.
	client := sdk.NewClient(&sdk.Implementation{Name: "fake"}, nil)
	authed := &http.Client{Transport: bearer{r.MCP.Token}}
	s, err := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: r.MCP.URL, HTTPClient: authed}, nil)
	if err != nil {
		f.t.Fatalf("connect with token: %v", err)
	}
	list, err := s.ListTools(ctx, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, tool := range list.Tools {
		f.tools = append(f.tools, tool.Name)
	}
	_ = s.Close()
	_, err = sdk.NewClient(&sdk.Implementation{Name: "x"}, nil).Connect(ctx, &sdk.StreamableClientTransport{Endpoint: r.MCP.URL}, nil)
	f.tokenOK = err != nil
	return exec.CommandContext(ctx, "/bin/sh", "-c", `printf '%s|' "$(pwd)"; ls -A; cat`), nil
}

func (f *fakeCLI) Result(_ CLIRun, stdout, _ []byte, runErr error) (*Output, error) {
	return &Output{Text: string(stdout)}, runErr
}

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func TestCLIGivesToolsWhatTheyNeed(t *testing.T) {
	fake := &fakeCLI{t: t}
	tool := Tool{
		Name: "secret_word", InputSchema: map[string]any{"type": "object"},
		Call: func(context.Context, map[string]any) (*sdk.CallToolResult, error) { return textResult("x", false), nil },
	}
	src := "---\nmodel: anthropic/claude-haiku-5-5\n---\n{{role \"system\"}}Be {{> memories}}.\n{{role \"user\"}}Today is {{today}}."
	out, err := (&CLI{Tool: fake}).Run(context.Background(), Job{
		Name: "t", Source: src, Input: map[string]any{"today": "Wednesday"},
		Partials: map[string]string{"memories": "brief"}, Tools: []Tool{tool}, MaxTurns: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	r := fake.run
	if strings.TrimSpace(r.System) != "Be brief." || strings.TrimSpace(r.User) != "Today is Wednesday." || r.Model != "anthropic/claude-haiku-5-5" || r.MaxTurns != 7 || r.MCP.Name != CLIServer() {
		t.Fatalf("run = %+v", r)
	}
	if strings.Join(fake.tools, ",") != "secret_word" || !fake.tokenOK {
		t.Fatalf("tools %v, unauthenticated connect refused: %v", fake.tools, fake.tokenOK)
	}
	dir, rest, _ := strings.Cut(out.Text, "|")
	if !strings.Contains(dir, "procauto-run-") || rest != "Today is Wednesday." {
		t.Fatalf("output %q: want an empty temp dir and the user prompt on stdin", out.Text)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("working dir %s not removed", dir)
	}
}

func TestEffortReachesClaudeCode(t *testing.T) {
	args := func(effort string) string {
		cmd, err := (&ClaudeCode{Path: "/bin/claude"}).Command(context.Background(), CLIRun{Effort: effort, MCP: MCPEndpoint{Name: "procauto"}})
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(cmd.Args, " ")
	}
	if !strings.Contains(args("high"), "--effort high") || strings.Contains(args(""), "--effort") {
		t.Fatal("--effort only when the routine sets one")
	}
	if effort("default") != "" || effort("max") != "max" {
		t.Fatal(`"default" means no effort`)
	}
	if _, err := (&Genkit{}).Run(context.Background(), Job{Effort: "high"}); err == nil || !strings.Contains(err.Error(), "effort") {
		t.Fatalf("claude-api backend must reject effort: %v", err)
	}
}

// hangingCLI is an agent CLI that leaves a process holding its output.
type hangingCLI struct{ fakeCLI }

func (*hangingCLI) Command(ctx context.Context, _ CLIRun) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, "/bin/sh", "-c", "sleep 60 & wait"), nil
}

// A run that's stopped ends once its CLI is killed, even with something it
// started still holding its output (an MCP server, a tool's command).
func TestAStoppedCLIRunEndsDespiteWhatItStarted(t *testing.T) {
	orig := cliWaitDelay
	cliWaitDelay = 200 * time.Millisecond
	t.Cleanup(func() { cliWaitDelay = orig })
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := (&CLI{Tool: &hangingCLI{}}).Run(ctx, Job{Name: "t", Source: "Say hi."}); err == nil {
		t.Error("a killed CLI ran fine")
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("took %s", took)
	}
}
