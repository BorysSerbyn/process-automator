package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/programs"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

func guideOut(t *testing.T, args ...string) string {
	t.Helper()
	out, err := cli(t, filepath.Join(t.TempDir(), "config.yaml"), append([]string{"guide"}, args...)...)
	if err != nil {
		t.Fatalf("guide %v: %v\n%s", args, err, out)
	}
	return out
}

func TestGuideListsTopics(t *testing.T) {
	out := guideOut(t)
	var got GuideOutput
	if err := json.Unmarshal([]byte(guideOut(t, "--json")), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Topics) != len(guideTopics) || got.Sections != nil {
		t.Fatalf("--json: %+v", got)
	}
	for i, topic := range guideTopics {
		if !strings.Contains(out, topic.Name+" ") || !strings.Contains(out, topic.Summary) || got.Topics[i].Name != topic.Name {
			t.Errorf("topic %s missing:\n%s", topic.Name, out)
		}
	}
	if _, err := cli(t, filepath.Join(t.TempDir(), "c.yaml"), "guide", "nope"); err == nil {
		t.Error("an unknown topic was shown")
	}
}

// Every topic renders, and its JSON is the same content as its text.
func TestGuideJSON(t *testing.T) {
	for _, topic := range guideTopics {
		var g GuideOutput
		if err := json.Unmarshal([]byte(guideOut(t, topic.Name, "--json")), &g); err != nil {
			t.Fatal(err)
		}
		if g.Topic != topic.Name || g.Title == "" || len(g.Sections) < 2 || g.Topics != nil {
			t.Fatalf("%s: %+v", topic.Name, g)
		}
		text := guideOut(t, topic.Name)
		for _, s := range g.Sections {
			if s.Title == "" || s.Text == "" || !strings.Contains(text, s.Text) {
				t.Errorf("%s: section %q isn't in the text", topic.Name, s.Title)
			}
			for _, tbl := range s.Tables {
				for _, row := range tbl.Rows {
					if len(row) != len(tbl.Columns) {
						t.Errorf("%s: table %s: row %v doesn't match its columns %v", topic.Name, tbl.ID, row, tbl.Columns)
					}
				}
			}
		}
		if strings.Contains(text, "%{") {
			t.Errorf("%s: a placeholder is left in the text", topic.Name)
		}
	}
}

func TestGuideRoutinesCoversTheCode(t *testing.T) {
	out := guideOut(t, "routines")
	var want []string
	for _, f := range routines.Fields {
		want = append(want, "`automator."+f.Name)
	}
	for _, f := range settings.RoutineFields {
		want = append(want, "`automator."+f.Name+"`")
	}
	for name, b := range engine.Backends {
		want = append(want, "`"+name+"`")
		for _, p := range b.ModelProviders {
			want = append(want, p+"/<model>")
		}
		if b.Builtins != nil {
			for _, tool := range b.Builtins.Tools {
				want = append(want, "`"+tool.Name+"` (backend: "+name)
			}
		}
	}
	want = append(want, routines.Platforms...)
	want = append(want, "routines create", "run <name> --dry-run", "routines trust <name> --resume", "routines check", "routines update",
		"report_outcome", "{{> memories}}", "{{today}}", "{{since}}", `{{role "system"}}`, `{{role "user"}}`, "`output`", "notify_me", "mcp tools <server>")
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("guide routines lacks %s", w)
		}
	}
}

func TestGuidePluginsCoversTheCode(t *testing.T) {
	out := guideOut(t, "plugins")
	typ := reflect.TypeOf(pluginManifest{})
	for i := range typ.NumField() {
		if name := typ.Field(i).Tag.Get("yaml"); !strings.Contains(out, "- `"+name+"` (") {
			t.Errorf("guide plugins lacks plugin.yaml's %s", name)
		}
	}
	server, _ := guideFiles.ReadFile("guide/testdata/notes-server/main.go")
	for _, w := range []string{string(server), "--url", "--command", "--dir", "--auth", "--header", "--arg", "--env",
		"auth login <name>", "plugins check <name>", "mcp tools <name>", "readOnlyHint", "pairing"} {
		if !strings.Contains(out, w) {
			t.Errorf("guide plugins lacks %q", w)
		}
	}
}

func TestGuideProgramsCoversTheCode(t *testing.T) {
	out := guideOut(t, "programs")
	want := []string{"$" + programs.ResultEnv, "$" + programs.TriggerEnv, "$" + programs.ForEnv, "$" + programs.RunIDEnv,
		programs.DryRunFlag, programs.DefaultPath, "routines create <name> --kind program", "auth login <name>",
		"routines trust <name> --resume", "run <name> --dry-run", "routines update <name>"}
	for _, f := range programs.Fields {
		want = append(want, "`"+f.Name+"` (")
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("guide programs lacks %s", w)
		}
	}
	src, _ := guideFiles.ReadFile("guide/programs.md")
	example := strings.SplitN(strings.SplitN(string(src), "```yaml\n", 2)[1], "```", 2)[0]
	if _, err := programs.Parse("backup", example); err != nil {
		t.Errorf("the example program.yaml doesn't parse: %v", err)
	}
}

// The examples work: the notes server builds and installs as a plugin folder
// (plugins add checks it), and the example routine parses and is created
// against it and a stand-in notify plugin, its tools checked.
func TestGuideExamplesWork(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the example's plugin.yaml runs ./server")
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("building the example server needs go on PATH: %v", err)
	}
	folder := t.TempDir()
	build := exec.Command(goTool, "build", "-o", filepath.Join(folder, "server"), "./guide/testdata/notes-server")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("the example server doesn't build: %v\n%s", err, out)
	}
	manifest, _ := guideFiles.ReadFile("guide/testdata/notes-server/plugin.yaml")
	if err := os.WriteFile(filepath.Join(folder, manifestFile), manifest, 0o600); err != nil {
		t.Fatal(err)
	}

	notify := pluginapi.Plugin{Name: "notify", InProcess: &pluginapi.InProcess{Serve: func(pluginapi.Host) (*sdk.Server, error) {
		s := sdk.NewServer(&sdk.Implementation{Name: "notify", Version: "1"}, nil)
		s.AddTool(&sdk.Tool{Name: "notify_me", InputSchema: map[string]any{"type": "object"}, Annotations: &sdk.ToolAnnotations{}},
			func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				return &sdk.CallToolResult{}, nil
			})
		return s, nil
	}}}
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	run := func(args ...string) string {
		t.Helper()
		root := NewRoot([]pluginapi.Plugin{notify})
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(append([]string{"--config", cfg}, args...))
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out.String())
		}
		return out.String()
	}
	if out := run("plugins", "add", "notes", "--dir", folder); !strings.Contains(out, "1 read-only, 1 write") {
		t.Fatalf("the example plugin: %s", out)
	}

	src, _ := guideFiles.ReadFile("guide/new-notes.prompt")
	r, err := routines.Parse("new-notes", string(src))
	if err != nil {
		t.Fatalf("the example routine doesn't parse: %v", err)
	}
	if !slices.Equal(r.Servers, []string{"notes", "notify"}) || len(r.Tools) == 0 {
		t.Fatalf("the example routine: %+v", r)
	}
	if out := run("routines", "create", "new-notes", "--source", string(src)); !strings.Contains(out, "paused") || strings.Contains(out, "warning") {
		t.Fatalf("the example routine isn't created cleanly: %s", out)
	}
}
