package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// pluginEnv makes the test binary a stdio MCP server: "good" (annotated
// tools), "unannotated" (a tool without annotations) or "empty" (no tools).
const pluginEnv = "PROCAUTO_TEST_AS_PLUGIN"

func servePluginFixture(mode string) {
	srv := sdk.NewServer(&sdk.Implementation{Name: "fixture", Version: "1"}, nil)
	noop := func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "ok"}}}, nil
	}
	schema := map[string]any{"type": "object"}
	if mode != "empty" {
		srv.AddTool(&sdk.Tool{Name: "read_thing", InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}}, noop)
		ann := &sdk.ToolAnnotations{}
		if mode == "unannotated" {
			ann = nil
		}
		srv.AddTool(&sdk.Tool{Name: "write_thing", InputSchema: schema, Annotations: ann}, noop)
	}
	_ = srv.Run(context.Background(), &sdk.StdioTransport{})
}

// pluginFolder writes a plugin folder whose server is the test binary in mode.
func pluginFolder(t *testing.T, mode string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	manifest := fmt.Sprintf("description: a %s fixture\ncommand: %s\nenv:\n  %s: %s\n", mode, exe, pluginEnv, mode)
	if err := os.WriteFile(filepath.Join(dir, manifestFile), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "README.md"), []byte("fixture"), 0o600)
	return dir
}

func TestPluginFolderTrial(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	installed := filepath.Join(filepath.Dir(cfg), "plugins", "fx")

	for _, mode := range []string{"unannotated", "empty"} {
		out, err := cli(t, cfg, "plugins", "add", "fx", "--dir", pluginFolder(t, mode))
		if err == nil || !strings.Contains(out, "nothing installed") {
			t.Fatalf("%s accepted: %s", mode, out)
		}
	}
	if _, err := os.Stat(installed); !os.IsNotExist(err) {
		t.Fatal("a rejected plugin left its folder")
	}
	if entries, _ := os.ReadDir(filepath.Dir(installed)); len(entries) != 0 {
		t.Fatalf("staging left behind: %v", entries)
	}

	out, err := cli(t, cfg, "plugins", "add", "fx", "--dir", pluginFolder(t, "good"))
	if err != nil || !strings.Contains(out, "read_thing") || !strings.Contains(out, "1 read-only, 1 write") {
		t.Fatalf("good plugin: %s %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(installed, "README.md")); err != nil {
		t.Fatal("folder not copied into the app")
	}
	if b, _ := os.ReadFile(cfg); !strings.Contains(string(b), "dir: "+installed) {
		t.Fatalf("config doesn't run the copy:\n%s", b)
	}
	if out, err := cli(t, cfg, "plugins", "check", "fx"); err != nil || !strings.Contains(out, "ok    fx") {
		t.Fatalf("check: %s %v", out, err)
	}

	// A broken new version is rejected and the working one stays.
	if _, err := cli(t, cfg, "plugins", "add", "fx", "--replace", "--dir", pluginFolder(t, "empty")); err == nil {
		t.Fatal("broken replacement accepted")
	}
	if out, err := cli(t, cfg, "plugins", "check", "fx"); err != nil {
		t.Fatalf("working version lost: %s", out)
	}

	if _, err := cli(t, cfg, "plugins", "remove", "fx"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(installed); !os.IsNotExist(err) {
		t.Fatal("remove left the folder")
	}
}

func TestPluginCommandTrial(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if out, err := cli(t, cfg, "plugins", "add", "nope", "--command", "/does/not/exist"); err == nil || !strings.Contains(out, "nothing added") {
		t.Fatalf("a server that doesn't start was added: %s", out)
	}
	if b, _ := os.ReadFile(cfg); strings.Contains(string(b), "nope") {
		t.Fatal("rejected server written to config")
	}
	// Servers I don't control: missing annotations are a warning, not a rejection.
	exe, _ := os.Executable()
	out, err := cli(t, cfg, "plugins", "add", "ext", "--command", exe, "--env", pluginEnv+"=unannotated")
	if err != nil || !strings.Contains(out, "warning: tools without annotations: write_thing") {
		t.Fatalf("external server: %s %v", out, err)
	}
}

func TestPluginPathsStayInside(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if _, err := cli(t, cfg, "plugins", "add", "rel", "--command", "./server"); err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Errorf("relative command accepted: %v", err)
	}
	escape := pluginFolder(t, "good")
	_ = os.WriteFile(filepath.Join(escape, manifestFile), []byte("command: ../outside\n"), 0o600)
	if _, err := cli(t, cfg, "plugins", "add", "esc", "--dir", escape); err == nil || !strings.Contains(err.Error(), "outside the folder") {
		t.Errorf("command outside the folder accepted: %v", err)
	}
	linked := pluginFolder(t, "good")
	_ = os.Symlink("/etc/hosts", filepath.Join(linked, "hosts"))
	if _, err := cli(t, cfg, "plugins", "add", "lnk", "--dir", linked); err == nil || !strings.Contains(err.Error(), "points outside") {
		t.Errorf("symlink outside the folder accepted: %v", err)
	}
	// A folder reached through a symlink installs fine.
	via := filepath.Join(t.TempDir(), "via")
	_ = os.Symlink(pluginFolder(t, "good"), via)
	if out, err := cli(t, cfg, "plugins", "add", "via", "--dir", via); err != nil {
		t.Errorf("folder behind a symlink: %s %v", out, err)
	}
}

// A routine whose tools or servers don't exist isn't written: its servers are
// asked, as a run would; one that can't be asked leaves a warning instead.
func TestRoutineToolsChecked(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if out, err := cli(t, cfg, "plugins", "add", "fx", "--dir", pluginFolder(t, "good")); err != nil {
		t.Fatalf("plugins add: %v\n%s", err, out)
	}
	file := filepath.Join(filepath.Dir(cfg), "routines", "r.prompt")
	src := func(server, tool string) string {
		return "---\nautomator.outcome: {done: It did it., nothingToDo: []}\nautomator.servers: [" + server + "]\ntools: [" + tool + "]\n---\nGo. {{> memories}}\n"
	}
	out, err := cli(t, cfg, "routines", "create", "r", "--source", src("fx", "missing_thing"))
	if err == nil || !strings.Contains(out, "prompt lists tools no connected server offers: missing_thing") {
		t.Fatalf("a missing tool: %v\n%s", err, out)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("a refused routine was written")
	}
	if out, err := cli(t, cfg, "routines", "create", "r", "--source", src("nope", "x")); err == nil || !strings.Contains(out, "uses nope, which no installed plugin or server provides") {
		t.Fatalf("an unknown server: %v\n%s", err, out)
	}
	if out, err := cli(t, cfg, "routines", "create", "r", "--source", src("fx", "read_thing")); err != nil || strings.Contains(out, "warning") {
		t.Fatalf("a real tool: %v\n%s", err, out)
	}
	before, _ := os.ReadFile(file)
	if out, err := cli(t, cfg, "routines", "update", "r", "--source", src("fx", "read_thing, missing_thing")); err == nil || !strings.Contains(out, "missing_thing") {
		t.Fatalf("update with a missing tool: %v\n%s", err, out)
	}
	if after, _ := os.ReadFile(file); string(after) != string(before) {
		t.Error("a refused update changed the routine")
	}
	// A server that can't be asked now: written, with a warning.
	other := filepath.Join(t.TempDir(), "config.yaml")
	withNotesServer(t, other)
	if out, err := cli(t, other, "routines", "create", "n", "--source", src("notes", "list_notes")); err != nil || !strings.Contains(out, "warning: n's tools weren't checked") {
		t.Fatalf("an unreachable server: %v\n%s", err, out)
	}
}
