//go:build unix

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/engine/enginetest"
	"github.com/BorysSerbyn/process-automator/core/internal/history"
	"github.com/BorysSerbyn/process-automator/core/internal/programs"
)

// programSource writes a program's description file and script; it returns
// the description file's path.
func programSource(t *testing.T, desc, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "prog"), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, programs.DescriptionFile)
	if err := os.WriteFile(path, []byte(desc), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// cliJSON runs a command with --json and decodes what it prints on stdout.
func cliJSON[T any](t *testing.T, cfg string, args ...string) T {
	t.Helper()
	root := NewRoot(nil)
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"--config", cfg}, append(args, "--json")...))
	if err := root.Execute(); err != nil {
		t.Fatalf("%v\n%s%s", err, out.String(), errOut.String())
	}
	var v T
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	return v
}

// A program, from adding it to deleting it, through the commands every
// routine has: no kind means an LLM routine wherever no name is given, and a
// name finds either kind.
func TestProgramCommands(t *testing.T) {
	a, _ := enginetest.Fixture(t, map[string]string{"standup": "queue"}, nil)
	cfg := a.Layout.Config()
	desc := programSource(t, "description: Backs up my notes\nexecutable: ./prog\nwrites: true\nschedule: \"0 9 * * *\"\n"+
		"secrets:\n  - name: api-token\n", "echo \"args: $*\"\necho 'Copied 3 files' > \"$PA_RESULT\"\n")

	// Adding: from the description file's path only.
	if out, err := cli(t, cfg, "routines", "create", "backup", "--kind", "program", "--source", "executable: ./prog"); err == nil {
		t.Fatalf("added a program from --source: %s", out)
	}
	if out, err := cli(t, cfg, "routines", "create", "backup", "--kind", "program", "--from", "-"); err == nil {
		t.Fatalf("added a program from stdin: %s", out)
	}
	added := cliJSON[RoutineChange](t, cfg, "routines", "create", "backup", "--kind", "program", "--from", desc)
	if added.Kind != "program" || added.Enabled == nil || *added.Enabled {
		t.Fatalf("%+v", added)
	}
	// Names are unique across kinds.
	if out, err := cli(t, cfg, "routines", "create", "backup", "--source", "Go."); err == nil || !strings.Contains(out, "already exists") {
		t.Fatalf("an LLM routine took a program's name: %v %s", err, out)
	}
	if out, err := cli(t, cfg, "routines", "create", "standup", "--kind", "program", "--from", desc); err == nil || !strings.Contains(out, "already exists") {
		t.Fatalf("a program took a routine's name: %v %s", err, out)
	}

	// Listing: LLM routines unless a kind is named.
	names := func(args ...string) string {
		t.Helper()
		rows := cliJSON[[]RoutineInfo](t, cfg, append([]string{"routines", "list"}, args...)...)
		var out []string
		for _, r := range rows {
			out = append(out, r.Name+":"+r.Kind)
		}
		return strings.Join(out, " ")
	}
	if got := names(); got != "standup:" {
		t.Fatalf("default list: %s", got)
	}
	if got := names("--kind", "program"); got != "backup:program" {
		t.Fatalf("programs: %s", got)
	}
	if got := names("--kind", "all"); got != "backup:program standup:" {
		t.Fatalf("all: %s", got)
	}
	if _, err := cli(t, cfg, "routines", "list", "--kind", "robot"); err == nil {
		t.Fatal("an unknown kind listed")
	}
	raw, err := cli(t, cfg, "routines", "list", "--json")
	if err != nil || strings.Contains(raw, `"kind"`) || strings.Contains(raw, `"program"`) {
		t.Fatalf("an LLM routine's row has a kind or program section: %s", raw)
	}

	show := cliJSON[RoutineDetail](t, cfg, "routines", "show", "backup")
	if p := show.Program; p == nil || !p.Writes || len(p.Secrets) != 1 || p.Secrets[0].Env != "API_TOKEN" || p.Secrets[0].Stored ||
		show.Description != "Backs up my notes" || show.Trusted || show.Schedule != "0 9 * * *" || !strings.Contains(show.Source, "writes: true") {
		t.Fatalf("%+v %+v", show, show.Program)
	}

	// LLM-only commands say what it is.
	if out, err := cli(t, cfg, "memory", "list", "--routine", "backup"); err == nil || !strings.Contains(out, "backup is a program") {
		t.Fatalf("memories of a program: %v %s", err, out)
	}
	if out, err := cli(t, cfg, "settings", "set", "routines.backup.effort", "high"); err == nil || !strings.Contains(out, "backup is a program") {
		t.Fatalf("an LLM setting on a program: %v %s", err, out)
	}
	if _, err := cli(t, cfg, "settings", "set", "routines.backup.timeout", "5m"); err != nil {
		t.Fatalf("a program's own setting: %v", err)
	}

	// Setup asks to turn it on, and to sign in to it (its secret).
	steps := map[string]engine.SetupStep{}
	for _, st := range cliJSON[engine.SetupOutput](t, cfg, "setup").Steps {
		steps[st.Step+" "+st.Subject] = st
	}
	if st, ok := steps["turn-on backup"]; !ok || st.Done || !strings.Contains(st.Ask, "Trust backup") {
		t.Fatalf("turn-on step: %+v", st)
	}
	if st, ok := steps["sign-in backup"]; !ok || st.Done {
		t.Fatalf("sign-in step: %+v", st)
	}

	// Trust: reviewed, then given; it runs only once trusted, dry runs too.
	if out, err := cli(t, cfg, "run", "backup", "--dry-run"); err == nil || !strings.Contains(out, "not trusted") {
		t.Fatalf("an untrusted program's dry run: %v %s", err, out)
	}
	review := cliJSON[TrustOutput](t, cfg, "routines", "trust", "backup", "--check")
	if review.Kind != "program" || review.Program == nil || review.Program.Checksum == "" || review.Trusted || len(review.Surface.Servers) != 0 {
		t.Fatalf("%+v", review)
	}
	if out, err := cli(t, cfg, "routines", "trust", "backup", "--yes", "--resume"); err != nil || !strings.Contains(out, "Resumed backup") {
		t.Fatalf("trust: %v %s", err, out)
	}

	// Running: a secret it needs is a sign-in.
	if _, err := cli(t, cfg, "run", "backup"); err == nil {
		t.Fatal("ran without its secret")
	}
	if err := enginetest.Secrets.Set(engine.ProgramSecretKey("backup", "api-token"), "tok"); err != nil {
		t.Fatal(err)
	}
	ran := cliJSON[RunOutput](t, cfg, "run", "backup", "--dry-run")
	if ran.Kind != "program" || ran.Program == nil || ran.Result != nil || !strings.Contains(ran.Program.Output, "args: --dry-run") {
		t.Fatalf("%+v %+v", ran, ran.Program)
	}
	ran = cliJSON[RunOutput](t, cfg, "run", "backup")
	if ran.Program.Summary != "Copied 3 files" {
		t.Fatalf("%+v", ran.Program)
	}
	runs := cliJSON[[]history.Summary](t, cfg, "runs", "backup")
	if len(runs) == 0 || runs[0].Kind != "program" || runs[0].Reason != "Copied 3 files" {
		t.Fatalf("%+v", runs)
	}
	if out, err := cli(t, cfg, "routines", "pause", "backup"); err != nil || !strings.Contains(out, "Paused backup") {
		t.Fatalf("pause: %v %s", err, out)
	}

	// Deleting forgets its secrets, settings and trust.
	if out, err := cli(t, cfg, "routines", "delete", "backup"); err != nil {
		t.Fatalf("delete: %v %s", err, out)
	}
	if _, err := enginetest.Secrets.Get(engine.ProgramSecretKey("backup", "api-token")); err == nil {
		t.Fatal("its secret outlived it")
	}
	if got := names("--kind", "all"); got != "standup:" {
		t.Fatalf("after delete: %s", got)
	}
}

// Through the MCP server Claude may list, run and stop programs, but not add
// or trust one.
func TestProgramsOverMCP(t *testing.T) {
	tools := toolMap(testServer(t, false))
	create := tools["routines_create"].tool.InputSchema.(map[string]any)["properties"].(map[string]any)
	if _, ok := create["kind"]; ok {
		t.Error("routines_create takes a kind over MCP: a client could add a program")
	}
	list := tools["routines_list"].tool.InputSchema.(map[string]any)["properties"].(map[string]any)
	if _, ok := list["kind"]; !ok {
		t.Error("routines_list can't list programs over MCP")
	}
	for _, name := range []string{"run", "stop", "start"} {
		if _, ok := tools[name]; !ok {
			t.Errorf("missing tool %s", name)
		}
	}
	if _, ok := tools["routines_trust"]; ok {
		t.Error("trusting is served over MCP")
	}
	// What a client's source comes as (stdin) can't add or update a program.
	argv, _, _ := tools["routines_update"].argv("/c.yaml", map[string]any{"routine": "backup", "source": "executable: ./x"})
	if !strings.Contains(strings.Join(argv, " "), "--from=-") {
		t.Fatalf("update's source isn't stdin: %v", argv)
	}
	a, _ := enginetest.Fixture(t, nil, nil)
	enginetest.AddProgram(t, a, "backup", "executable: ./prog\nwrites: false\n", "true\n", true)
	if out, err := cli(t, a.Layout.Config(), "routines", "update", "backup", "--from", "-"); err == nil || !strings.Contains(out, "description file's path") {
		t.Fatalf("a program updated from stdin: %v %s", err, out)
	}
}
