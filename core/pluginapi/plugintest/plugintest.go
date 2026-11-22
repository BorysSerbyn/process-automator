// Package plugintest runs in-process plugins over in-memory MCP, for their
// tests: a fake Host, and a session to the server a plugin builds with it.
package plugintest

import (
	"context"
	"fmt"
	"os"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BorysSerbyn/process-automator/core/internal/yamlfile"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// Host is a test pluginapi.Host. Each method answers from its field: Connect
// starts Servers[name] in memory, ConnectApp calls App (none: an error),
// AppName is Name ("Test App" when empty), Config decodes YAML strictly (as
// the engine does), Secret reads Secrets, DataDir is Data (made on first
// use; none: an error), RoutineConfig decodes Routine (the plugin's routine
// section, as a routine file would give it) strictly, and Run is a run in
// Folder.
type Host struct {
	Servers map[string]*sdk.Server
	// App opens a session to the app's MCP server: InMemory for a fake, or
	// one to the real server.
	App     func(ctx context.Context) (*sdk.ClientSession, error)
	Name    string
	YAML    string
	Secrets map[string]string
	Data    string
	Routine any
	Folder  string
}

var _ pluginapi.Host = (*Host)(nil)

// Connect implements pluginapi.Host.
func (h *Host) Connect(ctx context.Context, name string) (*sdk.ClientSession, error) {
	srv, ok := h.Servers[name]
	if !ok {
		return nil, fmt.Errorf("unknown plugin %q", name)
	}
	return Connect(ctx, srv)
}

// ConnectApp implements pluginapi.Host.
func (h *Host) ConnectApp(ctx context.Context) (*sdk.ClientSession, error) {
	if h.App == nil {
		return nil, fmt.Errorf("no app MCP server in this test")
	}
	return h.App(ctx)
}

// AppName implements pluginapi.Host.
func (h *Host) AppName() string {
	if h.Name == "" {
		return "Test App"
	}
	return h.Name
}

// Config implements pluginapi.Host.
func (h *Host) Config(v any) error { return yamlfile.DecodeStrict([]byte(h.YAML), v) }

// Secret implements pluginapi.Host.
func (h *Host) Secret(name string) (string, error) {
	v, ok := h.Secrets[name]
	if !ok {
		return "", fmt.Errorf("secret %q not set", name)
	}
	return v, nil
}

// DataDir implements pluginapi.Host.
func (h *Host) DataDir() (string, error) {
	if h.Data == "" {
		return "", fmt.Errorf("no data dir in this test")
	}
	return h.Data, os.MkdirAll(h.Data, 0o700)
}

// RoutineConfig implements pluginapi.Host.
func (h *Host) RoutineConfig(v any) error {
	if h.Routine == nil {
		return nil
	}
	return yamlfile.DecodeStrict(h.Routine, v)
}

// Run implements pluginapi.Host.
func (h *Host) Run() pluginapi.Run { return pluginapi.Run{Folder: h.Folder} }

// InMemory is a Host.App serving s in memory.
func InMemory(s *sdk.Server) func(context.Context) (*sdk.ClientSession, error) {
	return func(ctx context.Context) (*sdk.ClientSession, error) { return Connect(ctx, s) }
}

// Connect opens a client session to a server in memory.
func Connect(ctx context.Context, s *sdk.Server) (*sdk.ClientSession, error) {
	ct, st := sdk.NewInMemoryTransports()
	if _, err := s.Connect(ctx, st, nil); err != nil {
		return nil, err
	}
	return sdk.NewClient(&sdk.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
}

// Serve builds p's server with h and returns a session to it, closed when
// the test ends. Every tool must declare annotations (readOnlyHint), as dry
// runs depend on them.
func Serve(t testing.TB, p pluginapi.Plugin, h pluginapi.Host) *sdk.ClientSession {
	t.Helper()
	if p.InProcess == nil {
		t.Fatalf("%s isn't an in-process plugin", p.Name)
	}
	srv, err := p.InProcess.Serve(h)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := Connect(context.Background(), srv)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools.Tools {
		if tl.Annotations == nil {
			t.Errorf("%s: tool %s must declare annotations (readOnlyHint)", p.Name, tl.Name)
		}
	}
	return cs
}
