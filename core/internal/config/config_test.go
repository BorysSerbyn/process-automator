package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

var base = map[string]pluginapi.ServerSpec{
	"remote": {URL: "https://example.com/mcp", Scopes: []string{"read"}},
	"local":  {Command: "/bin/local-mcp"},
}

func TestLoadMissingFileGivesBase(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.yaml"), base)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MCPServers["remote"].URL == "" || len(cfg.MCPServers) != 2 {
		t.Fatalf("unexpected: %+v", cfg)
	}
}

func TestLoadOverridesAddsAndPluginSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(path, []byte(`
mcpServers:
  remote:
    url: https://dev.example.com/mcp
  extra:
    command: /usr/local/bin/extra-mcp
plugins:
  notes:
    provider: cloud
`), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, base)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.MCPServers["remote"]; got.URL != "https://dev.example.com/mcp" || len(got.Scopes) != 0 {
		t.Fatalf("remote not replaced: %+v", got)
	}
	if cfg.MCPServers["local"].Command == "" || cfg.MCPServers["extra"].Command == "" {
		t.Fatalf("base lost or extra missing: %+v", cfg.MCPServers)
	}
	pc := struct {
		Provider string `yaml:"provider"`
		Server   string `yaml:"server"`
	}{Provider: "local", Server: "files"}
	if err := cfg.DecodePlugin("notes", &pc); err != nil || pc.Provider != "cloud" || pc.Server != "files" {
		t.Fatalf("plugin section: %+v %v", pc, err)
	}
	if err := cfg.DecodePlugin("absent", &pc); err != nil {
		t.Fatal(err)
	}
	var strict struct {
		Server string `yaml:"server"`
	}
	if err := cfg.DecodePlugin("notes", &strict); err == nil {
		t.Fatal("unknown field (provider) should be rejected")
	}
}

func TestValidateRejectsAmbiguousServer(t *testing.T) {
	cfg := Config{MCPServers: map[string]pluginapi.ServerSpec{"bad": {Command: "x", URL: "https://y"}}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("want error")
	}
}

func TestLoadRejectsUnknownServerFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "mcpServers:\n  local:\n    command: /x\n    envSecrets: {A: b}\nsettings:\n  llm.backend: claude-code\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, nil); err == nil || !strings.Contains(err.Error(), "envSecrets") {
		t.Fatalf("want an error naming envSecrets, got %v", err)
	}
	if err := os.WriteFile(path, []byte("settings:\n  llm.backend: claude-code\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, nil); err != nil {
		t.Fatalf("settings-only config rejected: %v", err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, nil); err != nil {
		t.Fatalf("empty config rejected: %v", err)
	}
}

// A server in the file declares what the OS must allow it; an unknown
// permission is refused, and writing a server keeps its permissions.
func TestServerPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "mcpServers:\n  keys:\n    command: /x\n    permissions: [input-monitoring, accessibility]\n  plain:\n    command: /y\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, base)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Permissions["keys"]; strings.Join(got, ",") != "accessibility,input-monitoring" || cfg.MCPServers["keys"].Command != "/x" {
		t.Fatalf("%v %+v", got, cfg.MCPServers["keys"])
	}
	if got, ok := cfg.Permissions["plain"]; !ok || len(got) != 0 {
		t.Fatalf("a server that declares none: %v %v", got, ok)
	}
	if _, ok := cfg.Permissions["remote"]; ok {
		t.Fatal("a preset's permissions aren't the file's")
	}
	if err := os.WriteFile(path, []byte("mcpServers:\n  keys:\n    command: /x\n    permissions: [keyboard]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, nil); err == nil || !strings.Contains(err.Error(), `unknown permission "keyboard"`) {
		t.Fatalf("an unknown permission: %v", err)
	}
	if err := SetServer(path, "keys", Server{ServerSpec: pluginapi.ServerSpec{Command: "/z"}, Permissions: []string{"camera"}}); err != nil {
		t.Fatal(err)
	}
	if cfg, err := Load(path, nil); err != nil || strings.Join(cfg.Permissions["keys"], ",") != "camera" {
		t.Fatalf("written: %+v %v", cfg.Permissions, err)
	}
	if err := SetServer(path, "keys", Server{ServerSpec: pluginapi.ServerSpec{Command: "/z"}, Permissions: []string{"keyboard"}}); err == nil {
		t.Fatal("wrote an unknown permission")
	}
}
