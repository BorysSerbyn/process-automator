package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BorysSerbyn/process-automator/core/internal/mcp"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/secrets"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

func notesPlugin() pluginapi.Plugin {
	return pluginapi.Plugin{
		Name: "notes",
		InProcess: &pluginapi.InProcess{
			Secrets: []pluginapi.SecretSpec{{Name: "api-key", Prompt: "Token"}},
			Serve: func(h pluginapi.Host) (*sdk.Server, error) {
				var cfg struct {
					Space string `yaml:"space"`
				}
				return sdk.NewServer(&sdk.Implementation{Name: "notes"}, nil), h.Config(&cfg)
			},
		},
	}
}

func TestHostSecretsAreScopedToDeclaredNames(t *testing.T) {
	store := secrets.NewMemory()
	_ = store.Set(PluginSecretKey("notes", "api-key"), "tok")
	_ = store.Set(PluginSecretKey("other", "api-key"), "not-yours")
	h := &host{plugin: notesPlugin(), conn: &mcp.Connector{Secrets: store}}

	if v, err := h.Secret("api-key"); err != nil || v != "tok" {
		t.Fatalf("declared secret: %q %v", v, err)
	}
	if _, err := h.Secret("password"); err == nil {
		t.Fatal("undeclared secret readable")
	}
	_ = store.Delete(PluginSecretKey("notes", "api-key"))
	if _, err := h.Secret("api-key"); !errors.Is(err, mcp.ErrNeedsReauth) {
		t.Fatalf("missing secret should need re-auth, got %v", err)
	}
}

func loadWith(t *testing.T, yaml string, plugins ...pluginapi.Plugin) (*App, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path, plugins, secrets.NewMemory())
}

func TestConfigCannotShadowInProcessPlugin(t *testing.T) {
	_, err := loadWith(t, "mcpServers:\n  notes:\n    url: https://x\n", notesPlugin())
	if err == nil || !strings.Contains(err.Error(), "clashes") {
		t.Fatalf("want clash error, got %v", err)
	}
}

func TestPluginConfigIsStrict(t *testing.T) {
	a, err := loadWith(t, "plugins:\n  notes:\n    spcae: typo\n", notesPlugin())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Connector.Connect(context.Background(), "notes"); err == nil || !strings.Contains(err.Error(), "spcae") {
		t.Fatalf("typo in plugin config should fail, got %v", err)
	}
}

// Plugin sessions opened for a run know it; sessions a plugin opens to
// another plugin don't (they're not part of the run).
func TestRunReach(t *testing.T) {
	seen := map[string]string{}
	var mu sync.Mutex
	probe := func(name, next string) pluginapi.Plugin {
		return pluginapi.Plugin{Name: name, InProcess: &pluginapi.InProcess{RoutineSection: true, Serve: func(h pluginapi.Host) (*sdk.Server, error) {
			mu.Lock()
			seen[name] = h.Run().Folder
			mu.Unlock()
			s := sdk.NewServer(&sdk.Implementation{Name: name, Version: "1"}, nil)
			s.AddTool(&sdk.Tool{Name: name + "_t", InputSchema: map[string]any{"type": "object"}},
				func(ctx context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
					if next != "" {
						cs, err := h.Connect(ctx, next)
						if err != nil {
							return nil, err
						}
						_ = cs.Close()
					}
					return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "ok"}}}, nil
				})
			return s, nil
		}}}
	}
	a, err := Load(filepath.Join(t.TempDir(), "config.yaml"), []pluginapi.Plugin{probe("outer", "inner"), probe("inner", "")}, secrets.NewMemory())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cs, err := a.Connector.ConnectFor(ctx, "outer", RunOf(routines.Routine{Name: "r"}, "/the/folder", ""))
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	if _, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "outer_t"}); err != nil {
		t.Fatal(err)
	}
	if seen["outer"] != "/the/folder" || seen["inner"] != "" {
		t.Fatalf("run: %v (outer should see the run, inner shouldn't)", seen)
	}
}

func TestRoutineEnvironmentRules(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	a, err := Load(cfg, []pluginapi.Plugin{
		{Name: "noop", InProcess: &pluginapi.InProcess{Serve: func(pluginapi.Host) (*sdk.Server, error) { return nil, nil }}},
		{Name: "remote", External: &pluginapi.ServerSpec{URL: "https://example.com/mcp"}},
	}, secrets.NewMemory())
	if err != nil {
		t.Fatal(err)
	}
	for name, src := range map[string]string{
		"relative folder":     "---\nautomator.folder: some/dir\n---\nGo.",
		"section not taken":   "---\nautomator.servers: [noop]\nautomator.plugins.noop: {x: 1}\n---\nGo.",
		"section of external": "---\nautomator.servers: [remote]\nautomator.plugins.remote: {x: 1}\n---\nGo.",
	} {
		r, err := routines.Parse("r", src)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := a.RoutineConfig(r); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A routine's servers include those its plugins connect to.
func TestServersFollowPlugins(t *testing.T) {
	a := &App{Plugins: []pluginapi.Plugin{
		{Name: "reader", InProcess: &pluginapi.InProcess{Uses: func(pluginapi.Host) []string { return []string{"source"} }}},
		{Name: "source", InProcess: &pluginapi.InProcess{Uses: func(pluginapi.Host) []string { return []string{"reader"} }}}, // cycles end
	}}
	got := a.Servers(routines.Routine{Servers: []string{"remote", "reader"}})
	if want := []string{"remote", "reader", "source"}; !slices.Equal(got, want) {
		t.Errorf("servers %v, want %v", got, want)
	}
}
