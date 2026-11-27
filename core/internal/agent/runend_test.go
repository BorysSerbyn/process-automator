package agent

import (
	"context"
	"net/http"
	"os/exec"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BorysSerbyn/process-automator/core/internal/mcp"
)

// callingCLI is an agent CLI that calls the "wait" tool and then runs until
// it's killed.
type callingCLI struct{ fakeCLI }

func (*callingCLI) Command(ctx context.Context, r CLIRun) (*exec.Cmd, error) {
	go func() {
		authed := &http.Client{Transport: bearer{r.MCP.Token}}
		s, err := sdk.NewClient(&sdk.Implementation{Name: "fake"}, nil).Connect(context.Background(),
			&sdk.StreamableClientTransport{Endpoint: r.MCP.URL, HTTPClient: authed}, nil)
		if err != nil {
			return
		}
		_, _ = s.CallTool(context.Background(), &sdk.CallToolParams{Name: "wait", Arguments: map[string]any{}})
	}()
	return exec.CommandContext(ctx, "/bin/sh", "-c", "sleep 60"), nil
}

// waitingAgent runs callingCLI with an in-process plugin whose "wait" tool
// runs wait (as a plugin waiting on a server that never answers would).
func waitingAgent(wait func(ctx context.Context)) *Agent {
	conn := &mcp.Connector{Builtins: map[string]func(mcp.Run) (*sdk.Server, error){"plugin": func(mcp.Run) (*sdk.Server, error) {
		s := sdk.NewServer(&sdk.Implementation{Name: "plugin"}, nil)
		sdk.AddTool(s, &sdk.Tool{Name: "wait"}, func(ctx context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, struct{}, error) {
			wait(ctx)
			return nil, struct{}{}, ctx.Err()
		})
		return s, nil
	}}}
	return &Agent{Connector: conn, Backend: &CLI{Tool: &callingCLI{}}}
}

// runFor runs the agent until ctx ends and says how long the run took.
func runFor(ctx context.Context, t *testing.T, a *Agent) time.Duration {
	t.Helper()
	start := time.Now()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = a.Run(ctx, Request{Name: "t", Source: "Wait.", Servers: []string{"plugin"}, Tools: []string{"wait"}})
	}()
	select {
	case <-done:
		return time.Since(start)
	case <-time.After(30 * time.Second):
		t.Fatal("the run didn't end")
		return 0
	}
}

// A run's deadline ends the tool calls its agent CLI made, so a plugin
// waiting on something that never answers doesn't keep the run going.
func TestARunsEndEndsItsToolCalls(t *testing.T) {
	called, cancelled := make(chan struct{}), make(chan struct{})
	a := waitingAgent(func(ctx context.Context) {
		close(called)
		<-ctx.Done()
		close(cancelled)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if took := runFor(ctx, t, a); took > 10*time.Second {
		t.Errorf("took %s", took)
	}
	select {
	case <-called:
	default:
		t.Fatal("the tool wasn't called")
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Error("the tool call wasn't cancelled")
	}
}

// A plugin that ignores being cancelled still doesn't keep the run going:
// closing its session is given up on.
func TestARunEndsDespiteAPluginIgnoringItsEnd(t *testing.T) {
	orig := closeGrace
	closeGrace = 200 * time.Millisecond
	t.Cleanup(func() { closeGrace = orig })
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	a := waitingAgent(func(context.Context) { <-release })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if took := runFor(ctx, t, a); took > 10*time.Second {
		t.Errorf("took %s", took)
	}
}
