//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BorysSerbyn/process-automator/core/internal/engine/enginetest"
	"github.com/BorysSerbyn/process-automator/core/internal/history"
	"github.com/BorysSerbyn/process-automator/core/internal/hooks"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/watchers"
)

// hookFiles writes a file and, when body isn't empty, an executable named
// exe beside it; it returns the file's path.
func hookFiles(t *testing.T, file, source, exe, body string) string {
	t.Helper()
	dir := t.TempDir()
	if body != "" {
		if err := os.WriteFile(filepath.Join(dir, exe), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, file)
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A hook and its watcher, from adding them to deleting them: hooks are named
// explicitly, never among routines, and fire with a sample event.
func TestHookCommands(t *testing.T) {
	a, _ := enginetest.Fixture(t, map[string]string{"standup": "queue"}, nil)
	cfg := a.Layout.Config()
	watcher := hookFiles(t, watchers.DescriptionFile, "description: My calendar\nexecutable: ./watch\nmode: poll\ninterval: 30s\n", "watch", "true\n")
	program := hookFiles(t, hooks.DescriptionFile, "description: Opens Meet\nexecutable: ./act\nwrites: true\n"+
		"on: {watcher: cal, type: meeting.started}\nstaleness: 2m\n", "act", "echo \"args: $*\"\ncat\n")
	prompt := hookFiles(t, "brief.prompt", "---\ndescription: Briefs me\nautomator.on: {watcher: cal, type: meeting.started}\n"+
		"automator.builtinTools: [Bash]\n---\nBrief me on {{event.data.title}}.\n", "", "")

	// Watchers: added from a path only; it runs once trusted.
	if out, err := cli(t, cfg, "watchers", "add", "cal", "--from", "-"); err == nil {
		t.Fatalf("added a watcher from stdin: %s", out)
	}
	w := cliJSON[WatcherChange](t, cfg, "watchers", "add", "cal", "--from", watcher)
	if w.Watcher != "cal" || w.Enabled == nil || !*w.Enabled {
		t.Fatalf("%+v", w)
	}
	review := cliJSON[WatcherTrustOutput](t, cfg, "watchers", "trust", "cal", "--check")
	if review.Trusted || review.Program.Checksum == "" || review.Mode != "poll" {
		t.Fatalf("%+v", review)
	}
	if out, err := cli(t, cfg, "watchers", "trust", "cal", "--yes"); err != nil || !strings.Contains(out, "Trusted cal") {
		t.Fatalf("watchers trust: %v %s", err, out)
	}

	// Hooks: added off and untrusted, from a path only.
	added := cliJSON[HookChange](t, cfg, "hooks", "add", "open-meet", "--from", program)
	if added.Kind != "program" || added.Enabled == nil || *added.Enabled {
		t.Fatalf("%+v", added)
	}
	// Like routines create, it refuses servers and tools that don't exist.
	bogus := filepath.Join(t.TempDir(), "bogus.prompt")
	if err := os.WriteFile(bogus, []byte("---\nautomator.on: {watcher: cal}\nautomator.servers: [nowhere]\n---\nHi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := cli(t, cfg, "hooks", "add", "bogus", "--from", bogus); err == nil || !strings.Contains(out, "no installed plugin or server provides") {
		t.Fatalf("a hook with an unknown server was added: %v %s", err, out)
	}
	if out, err := cli(t, cfg, "hooks", "add", "brief", "--from", prompt); err != nil || !strings.Contains(out, "off and not trusted") {
		t.Fatalf("hooks add: %v %s", err, out)
	}
	// An LLM hook has memories of its own ({{> memories}} reads them); a
	// program hook doesn't.
	if out, err := cli(t, cfg, "memory", "add", "--routine", "brief", "Standups are in room 4"); err != nil {
		t.Fatalf("memory add for an LLM hook: %v %s", err, out)
	}
	if out, err := cli(t, cfg, "memory", "list", "--routine", "brief"); err != nil || !strings.Contains(out, "room 4") {
		t.Fatalf("memory list for an LLM hook: %v %s", err, out)
	}
	if out, err := cli(t, cfg, "memory", "add", "--routine", "open-meet", "x"); err == nil || !strings.Contains(out, "program hook") {
		t.Fatalf("memory add for a program hook: %v %s", err, out)
	}
	// Names are unique across routines, programs, hooks and watchers.
	for _, args := range [][]string{
		{"hooks", "add", "standup", "--from", prompt},
		{"hooks", "add", "cal", "--from", prompt},
		{"watchers", "add", "open-meet", "--from", watcher},
		{"routines", "create", "brief", "--source", "Go."},
	} {
		if out, err := cli(t, cfg, args...); err == nil || !strings.Contains(out, "already exists") {
			t.Errorf("%v took a name in use: %v %s", args, err, out)
		}
	}

	// Never among routines, even with --kind all.
	for _, kind := range []string{"llm", "all", "program"} {
		for _, r := range cliJSON[[]RoutineInfo](t, cfg, "routines", "list", "--kind", kind) {
			if r.Name == "open-meet" || r.Name == "brief" {
				t.Fatalf("a hook is among routines (--kind %s)", kind)
			}
		}
	}
	list := cliJSON[[]HookInfo](t, cfg, "hooks", "list")
	if len(list) != 2 || list[0].Name != "brief" || list[0].Kind != "" || list[1].Kind != "program" || list[1].On.Type != "meeting.started" ||
		list[1].Staleness != "2m" || list[1].Rate != 60 || list[1].Enabled || list[1].Trusted {
		t.Fatalf("hooks list: %+v", list)
	}
	if brief := cliJSON[HookDetail](t, cfg, "hooks", "show", "brief"); strings.Join(brief.BuiltinTools, ",") != "Bash" {
		t.Fatalf("an LLM hook's built-in tools: %+v", brief.BuiltinTools)
	}
	show := cliJSON[HookDetail](t, cfg, "hooks", "show", "open-meet")
	if len(show.BuiltinTools) != 0 || show.Watcher == nil || !show.Watcher.Trusted || show.Watcher.Interval != "30s" || len(show.Watcher.Hooks) != 2 || !strings.Contains(show.Source, "meeting.started") {
		t.Fatalf("hooks show: %+v %+v", show, show.Watcher)
	}

	// A routine's commands say what it is.
	if out, err := cli(t, cfg, "run", "open-meet"); err == nil || !strings.Contains(out, "open-meet is a hook") {
		t.Fatalf("run on a hook: %v %s", err, out)
	}
	if out, err := cli(t, cfg, "routines", "show", "open-meet"); err == nil || !strings.Contains(out, "hooks show open-meet") {
		t.Fatalf("routines show on a hook: %v %s", err, out)
	}

	// Trying it out: a sample event; a program hook needs trust even for a dry run.
	sample := hookFiles(t, "sample.json", `{"id": "m1", "type": "meeting.started", "data": {"title": "Standup"}}`, "", "")
	if out, err := cli(t, cfg, "hooks", "fire", "open-meet", "--event", sample, "--dry-run"); err == nil || !strings.Contains(out, "not trusted") {
		t.Fatalf("an untrusted program hook fired: %v %s", err, out)
	}
	fired := cliJSON[RunOutput](t, cfg, "hooks", "fire", "brief", "--event", sample, "--dry-run")
	if fired.Result == nil || fired.Kind != "" {
		t.Fatalf("an LLM hook's dry run: %+v", fired)
	}
	if _, err := cli(t, cfg, "hooks", "fire", "brief", "--dry-run"); err == nil {
		t.Fatal("fired without an event")
	}
	if out, err := cli(t, cfg, "hooks", "fire", "brief", "--event-json", `{"type": "x"}`, "--dry-run"); err == nil || !strings.Contains(out, "needs an id") {
		t.Fatalf("a sample without an id: %v %s", err, out)
	}

	// Trust: what it acts on, its watcher, and a warning for an LLM hook that writes.
	trustOut := cliJSON[TrustOutput](t, cfg, "hooks", "trust", "brief", "--check")
	if !trustOut.Hook || trustOut.Trigger == nil || trustOut.Trigger.Watcher != "cal" || trustOut.Watcher == nil || !trustOut.Watcher.Trusted ||
		len(trustOut.Warnings) != 1 || !strings.Contains(trustOut.Warnings[0], "Bash") {
		t.Fatalf("hooks trust --check: %+v", trustOut)
	}
	// A program hook's review is what it runs, not an LLM routine's surface.
	progReview := cliJSON[TrustOutput](t, cfg, "hooks", "trust", "open-meet", "--check")
	if progReview.Program == nil || progReview.Program.Checksum == "" {
		t.Fatalf("a program hook's review lacks what it runs: %+v", progReview)
	}
	if out, _ := cli(t, cfg, "hooks", "trust", "open-meet", "--check"); !strings.Contains(out, "sha-256") || strings.Contains(out, "can reach") {
		t.Fatalf("a program hook's review: %s", out)
	}
	if out, err := cli(t, cfg, "hooks", "trust", "open-meet", "--yes", "--resume"); err != nil || !strings.Contains(out, "Turned open-meet on") {
		t.Fatalf("hooks trust --resume: %v %s", err, out)
	}
	ran := cliJSON[RunOutput](t, cfg, "hooks", "fire", "open-meet", "--event", sample, "--dry-run")
	if ran.Program == nil || !strings.Contains(ran.Program.Output, "args: --dry-run") || !strings.Contains(ran.Program.Output, `"id":"m1"`) {
		t.Fatalf("a program hook's dry run: %+v", ran.Program)
	}
	ran = cliJSON[RunOutput](t, cfg, "hooks", "fire", "open-meet", "--event", sample)
	runs := cliJSON[[]history.Summary](t, cfg, "runs", "open-meet")
	if len(runs) != 1 || !runs[0].Hook || runs[0].Trigger != "manual" || runs[0].Kind != "program" {
		t.Fatalf("runs: %+v", runs)
	}
	status := cliJSON[runstatus.Status](t, cfg, "status")
	if len(status.Hooks) != 2 {
		t.Fatalf("status hooks: %+v", status.Hooks)
	}
	for _, r := range status.Routines {
		if r.Name == "open-meet" || r.Name == "brief" {
			t.Fatalf("a hook among run status's routines: %+v", status.Routines)
		}
	}

	// Pausing, by its own command or a routine's: it's found by name.
	if p := cliJSON[HookChange](t, cfg, "hooks", "pause", "open-meet"); p.Enabled == nil || *p.Enabled {
		t.Fatalf("hooks pause: %+v", p)
	}
	if r := cliJSON[RoutineChange](t, cfg, "routines", "resume", "open-meet"); !r.Hook || r.Enabled == nil || !*r.Enabled {
		t.Fatalf("routines resume on a hook: %+v", r)
	}
	if out, err := cli(t, cfg, "settings", "set", "hooks.open-meet.rate", "5"); err != nil {
		t.Fatalf("a hook's setting: %v %s", err, out)
	}
	for _, s := range cliJSON[[]settings.State](t, cfg, "settings", "list", "--scope", "app") {
		if strings.HasPrefix(s.Key, "hooks.") || strings.HasPrefix(s.Key, "watchers.") {
			t.Fatalf("a hook's setting among the app's: %s", s.Key)
		}
	}
	mine := cliJSON[[]settings.State](t, cfg, "settings", "list", "--routine", "open-meet")
	if len(mine) == 0 || !strings.HasPrefix(mine[0].Key, "hooks.open-meet.") {
		t.Fatalf("settings list --routine on a hook: %+v", mine)
	}
	if p := cliJSON[WatcherChange](t, cfg, "watchers", "pause", "cal"); p.Enabled == nil || *p.Enabled {
		t.Fatalf("watchers pause: %+v", p)
	}

	// Deleting.
	if out, err := cli(t, cfg, "hooks", "delete", "open-meet"); err != nil {
		t.Fatalf("hooks delete: %v %s", err, out)
	}
	if out, err := cli(t, cfg, "watchers", "delete", "cal"); err != nil {
		t.Fatalf("watchers delete: %v %s", err, out)
	}
	if got := cliJSON[[]HookInfo](t, cfg, "hooks", "list"); len(got) != 1 || got[0].Problem == "" {
		t.Fatalf("after deleting: %+v", got)
	}
}

// Through the MCP server Claude may list, show, fire (dry runs too), pause
// and stop hooks and watchers, but never add or trust one.
func TestHooksOverMCP(t *testing.T) {
	tools := toolMap(testServer(t, false))
	for _, name := range []string{"hooks_list", "hooks_show", "hooks_fire", "hooks_fire_dry_run", "hooks_pause", "hooks_resume", "stop",
		"watchers_list", "watchers_show", "watchers_pause", "watchers_resume"} {
		if _, ok := tools[name]; !ok {
			t.Errorf("missing tool %s", name)
		}
	}
	for _, name := range []string{"hooks_add", "hooks_update", "hooks_trust", "watchers_add", "watchers_update", "watchers_trust"} {
		if _, ok := tools[name]; ok {
			t.Errorf("%s is served over MCP", name)
		}
	}
	fire := tools["hooks_fire"].tool.InputSchema.(map[string]any)["properties"].(map[string]any)
	if _, ok := fire["event"]; ok {
		t.Error("hooks_fire reads a local file over MCP")
	}
	argv, stdin, _ := tools["hooks_fire"].argv("/c.yaml", map[string]any{"hook": "x", "event_json": `{"id":"1","type":"t"}`})
	if !strings.Contains(strings.Join(argv, " "), "--event=-") || !strings.Contains(stdin, `"id":"1"`) {
		t.Fatalf("hooks_fire's event isn't sent on stdin: %v %q", argv, stdin)
	}
}
