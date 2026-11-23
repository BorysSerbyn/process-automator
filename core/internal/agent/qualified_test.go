package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BorysSerbyn/process-automator/core/internal/mcp"
)

// twoAccounts serves two accounts of one chat plugin, "chat" and
// "chat-work": both offer search (read-only) and post; only chat offers
// react. Each tool answers with the account it's from.
func twoAccounts(dryRun bool, s script) *Agent {
	server := func(account string, extra ...string) func(mcp.Run) (*sdk.Server, error) {
		return func(mcp.Run) (*sdk.Server, error) {
			srv := sdk.NewServer(&sdk.Implementation{Name: account}, nil)
			for _, name := range append([]string{"search", "post"}, extra...) {
				var ann *sdk.ToolAnnotations
				if name == "search" {
					ann = &sdk.ToolAnnotations{ReadOnlyHint: true}
				}
				sdk.AddTool(srv, &sdk.Tool{Name: name, Description: "Does " + name, Annotations: ann}, func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, any, error) {
					return textResult(account, false), nil, nil
				})
			}
			return srv, nil
		}
	}
	return &Agent{
		Connector: &mcp.Connector{Builtins: map[string]func(mcp.Run) (*sdk.Server, error){
			"chat": server("chat", "react"), "chat-work": server("chat-work"),
		}},
		Backend: s, DryRun: dryRun,
	}
}

var bothAccounts = []string{"chat", "chat-work"}

func TestAToolTwoServersOfferMustNameItsServer(t *testing.T) {
	ag := twoAccounts(false, nil)
	_, err := CheckTools(context.Background(), ag.Connector, mcp.Run{}, bothAccounts, []string{"search", "react"})
	var wrong *ToolsError
	if !errors.As(err, &wrong) || !strings.Contains(err.Error(), "chat/search or chat-work/search") {
		t.Fatalf("an ambiguous tool: %v", err)
	}
	got, err := CheckTools(context.Background(), ag.Connector, mcp.Run{}, bothAccounts, []string{"chat/search", "chat-work/search", "react"})
	if err != nil {
		t.Fatal(err)
	}
	want := []CheckedTool{
		{Name: "chat-work/search", Server: "chat-work", ModelName: "chat-work__search", ReadOnly: true},
		{Name: "chat/search", Server: "chat", ModelName: "chat__search", ReadOnly: true},
		{Name: "react", Server: "chat"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tool %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
	// server/tool names that server's tool, even when no other offers it.
	if _, err := CheckTools(context.Background(), ag.Connector, mcp.Run{}, bothAccounts, []string{"chat-work/react"}); err == nil || !strings.Contains(err.Error(), "chat-work/react") {
		t.Fatalf("a tool its server doesn't offer: %v", err)
	}
}

func TestToolNames(t *testing.T) {
	for _, ok := range [][]string{{"search"}, {"chat/search", "chat-work/search"}, {"chat/search", "react"}} {
		if err := CheckToolNames(bothAccounts, ok); err != nil {
			t.Errorf("%v: %v", ok, err)
		}
	}
	for _, bad := range [][]string{{"other/search"}, {"chat/"}, {"/search"}, {"chat/a/b"}, {"chat/search", "chat__search"}, {"react", "react"}} {
		if err := CheckToolNames(bothAccounts, bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

// The model sees each account's tool under a name that says the server, and
// each reaches its own server; calls and dry-run writes are recorded with it.
func TestEachAccountsToolReachesItsServer(t *testing.T) {
	var seen []string
	tools := []string{"chat/search", "chat-work/search", "chat/post", "chat-work/post"}
	for _, dryRun := range []bool{false, true} {
		got := map[string]string{}
		ag := twoAccounts(dryRun, nil)
		ag.Backend = backendFunc(func(ctx context.Context, j Job) (*Output, error) {
			for _, tl := range j.Tools {
				if tl.Name == OutcomeTool {
					continue
				}
				seen = append(seen, tl.Name+": "+tl.Description)
				out, err := tl.Call(ctx, nil)
				if err != nil {
					return nil, err
				}
				got[tl.Name] = textOf(out)
			}
			return &Output{}, nil
		})
		res, _ := ag.Run(context.Background(), Request{Name: "r", Servers: bothAccounts, Tools: tools})
		if got["chat-work__search"] != "chat-work" || got["chat__search"] != "chat" {
			t.Fatalf("reads went to %v", got)
		}
		if !dryRun && (got["chat-work__post"] != "chat-work" || got["chat__post"] != "chat") {
			t.Fatalf("writes went to %v", got)
		}
		if len(res.Writes) != 2 {
			t.Fatalf("writes: %+v", res.Writes)
		}
		for _, w := range res.Writes {
			if w.Tool != w.Server+"/post" {
				t.Errorf("write recorded as %s on %s", w.Tool, w.Server)
			}
		}
		for _, c := range res.Calls {
			if !strings.HasPrefix(c.Tool, c.Server+"/") {
				t.Errorf("call recorded as %s on %s", c.Tool, c.Server)
			}
		}
	}
	if !strings.Contains(strings.Join(seen, "\n"), "chat-work__search: [chat-work] Does search") {
		t.Errorf("descriptions don't say the server:\n%s", strings.Join(seen, "\n"))
	}
}

type backendFunc func(ctx context.Context, j Job) (*Output, error)

func (backendFunc) Name() string { return "func" }

func (f backendFunc) Run(ctx context.Context, j Job) (*Output, error) { return f(ctx, j) }

func textOf(r *sdk.CallToolResult) string {
	if len(r.Content) == 0 {
		return ""
	}
	if t, ok := r.Content[0].(*sdk.TextContent); ok {
		return t.Text
	}
	return ""
}

// An agent CLI (claude-code, codex) gets them from the gateway's one server,
// under the same names.
func TestAgentCLIsSeeEachAccountsTool(t *testing.T) {
	fake := &fakeCLI{t: t}
	ag := twoAccounts(false, nil)
	ag.Backend = &CLI{Tool: fake}
	src := "---\n---\n{{role \"user\"}}Search both."
	_, _ = ag.Run(context.Background(), Request{Name: "r", Source: src, Servers: bothAccounts, Tools: []string{"chat/search", "chat-work/search"}})
	got := strings.Join(fake.tools, ",")
	if !strings.Contains(got, "chat__search") || !strings.Contains(got, "chat-work__search") {
		t.Fatalf("the CLI's tools: %s", got)
	}
}
