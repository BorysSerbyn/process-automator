package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/secrets"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func notesPlugin() pluginapi.Plugin {
	return pluginapi.Plugin{
		Name:    "notes",
		Secrets: []pluginapi.SecretSpec{{Name: "api-key", Prompt: "Token"}},
		Serve: func(pluginapi.Host) (*sdk.Server, error) {
			return sdk.NewServer(&sdk.Implementation{Name: "notes"}, nil), nil
		},
	}
}

func TestLogoutForgetsInProcessPluginSecrets(t *testing.T) {
	a, err := loadTest(filepath.Join(t.TempDir(), "config.yaml"), []pluginapi.Plugin{notesPlugin()})
	if err != nil {
		t.Fatal(err)
	}
	store := secrets.NewMemory()
	_ = store.Set(pluginapi.SecretKey("notes", "api-key"), "tok")
	a.Connector.Secrets = store

	if err := logout(a, "notes"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(pluginapi.SecretKey("notes", "api-key")); !errors.Is(err, secrets.ErrNotFound) {
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
	a, err := loadTest(cfg, nil)
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
	if n[automatorServerName] != 1 || n[engine.NotifyServer] != 1 {
		t.Fatalf("the core's own plugins registered %v times", n)
	}
}
