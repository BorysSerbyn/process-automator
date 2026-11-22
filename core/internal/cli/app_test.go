package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/secrets"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

func notesPlugin() pluginapi.Plugin {
	return pluginapi.Plugin{
		Name: "notes",
		InProcess: &pluginapi.InProcess{
			Secrets: []pluginapi.SecretSpec{{Name: "api-key", Prompt: "Token"}},
			Serve: func(pluginapi.Host) (*sdk.Server, error) {
				return sdk.NewServer(&sdk.Implementation{Name: "notes"}, nil), nil
			},
		},
	}
}

func TestLogoutForgetsInProcessPluginSecrets(t *testing.T) {
	a, err := loadTest(filepath.Join(t.TempDir(), "config.yaml"), []pluginapi.Plugin{notesPlugin()})
	if err != nil {
		t.Fatal(err)
	}
	store := secrets.NewMemory()
	_ = store.Set(engine.PluginSecretKey("notes", "api-key"), "tok")
	a.Connector.Secrets = store

	if err := logout(a, "notes"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(engine.PluginSecretKey("notes", "api-key")); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("secret still stored: %v", err)
	}
}

func TestLogoutForgetsHeaderSecrets(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	body := "mcpServers:\n  notion:\n    url: https://example.com/mcp\n    auth: headers\n    headers:\n      Authorization: \"Bearer {notion-api-key}\"\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := loadTest(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	store := secrets.NewMemory()
	_ = store.Set("notion-api-key", "sk")
	a.Connector.Secrets = store
	if err := logout(a, "notion"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("notion-api-key"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("key still stored: %v", err)
	}
}

// The daemon reloads with the previous app's plugins, which include the
// core's own automator and notify servers: they must not register twice.
func TestReloadWithAppPlugins(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	other := notesPlugin()
	other.Name = "other"
	a, err := loadTest(cfg, []pluginapi.Plugin{notesPlugin(), other})
	if err != nil {
		t.Fatal(err)
	}
	b, err := loadTest(cfg, a.Plugins)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	n := map[string]int{}
	for _, p := range b.Plugins {
		n[p.Name]++
	}
	if len(n) != 2 || n["notes"] != 1 || n["other"] != 1 {
		t.Fatalf("plugins after a reload: %v", n)
	}
}

// A plugin reaches the app's own MCP server, read-only, through
// Host.ConnectApp: the real server (this test binary as the CLI), so the
// tools plugins use are checked by name and shape.
func TestPluginConnectsToTheApp(t *testing.T) {
	t.Setenv(cliEnv, "1") // the server, and the commands it runs, are this binary
	type seen struct {
		tools []string
		runs  string
		err   error
	}
	got := make(chan seen, 1)
	probe := pluginapi.Plugin{Name: "probe", InProcess: &pluginapi.InProcess{Serve: func(h pluginapi.Host) (*sdk.Server, error) {
		var s seen
		defer func() { got <- s }()
		ctx := context.Background()
		app, err := h.ConnectApp(ctx)
		if err != nil {
			s.err = err
			return sdk.NewServer(&sdk.Implementation{Name: "probe"}, nil), nil
		}
		defer func() { _ = app.Close() }()
		for tool, err := range app.Tools(ctx, nil) {
			if err != nil {
				s.err = err
				break
			}
			s.tools = append(s.tools, tool.Name)
		}
		res, err := app.CallTool(ctx, &sdk.CallToolParams{Name: "runs", Arguments: map[string]any{"limit": 0}})
		if err != nil || res.IsError {
			s.err = fmt.Errorf("runs: %v %s", err, pluginapi.ResultText(res))
		} else {
			s.runs = pluginapi.ResultText(res)
		}
		return sdk.NewServer(&sdk.Implementation{Name: "probe"}, nil), nil
	}}}
	a, err := loadTest(filepath.Join(t.TempDir(), "config.yaml"), []pluginapi.Plugin{probe})
	if err != nil {
		t.Fatal(err)
	}
	s, err := a.Connector.Connect(context.Background(), "probe")
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	r := <-got
	if r.err != nil {
		t.Fatal(r.err)
	}
	for _, want := range []string{"runs", "routines_list"} {
		if !slices.Contains(r.tools, want) {
			t.Errorf("no %s among %v", want, r.tools)
		}
	}
	for _, write := range []string{"run", "routines_create", "settings_set"} {
		if slices.Contains(r.tools, write) {
			t.Errorf("read-only server offers %s", write)
		}
	}
	if strings.TrimSpace(r.runs) != "[]" {
		t.Errorf("runs on an empty app: %q", r.runs)
	}
}
