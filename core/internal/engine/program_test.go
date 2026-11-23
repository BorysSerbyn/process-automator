//go:build unix

package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/engine/enginetest"
	"github.com/BorysSerbyn/process-automator/core/internal/history"
	"github.com/BorysSerbyn/process-automator/core/internal/notifications"
	"github.com/BorysSerbyn/process-automator/core/internal/programs"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
)

// run runs a routine of either kind and waits for it.
func run(t *testing.T, a *engine.App, name string, o engine.RunOptions) (*engine.RunResult, error) {
	t.Helper()
	x, err := a.Find(name)
	if err != nil {
		t.Fatal(err)
	}
	return a.Run(context.Background(), x, o)
}

// latest is a routine's latest run in history.
func latest(t *testing.T, a *engine.App, name string) *history.Entry {
	t.Helper()
	runs, err := history.List(a.Layout.History(), history.Filter{Routine: name, Limit: 1})
	if err != nil || len(runs) == 0 {
		t.Fatalf("no runs of %s: %v", name, err)
	}
	e, err := history.Get(a.Layout.History(), runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// One lookup finds both kinds; listing takes the kinds asked for; a name both
// kinds have is broken for both.
func TestFindAcrossKinds(t *testing.T) {
	a, _ := enginetest.Fixture(t, map[string]string{"standup": "queue"}, nil)
	enginetest.AddProgram(t, a, "backup", "executable: ./prog\nwrites: false\n", "true\n", true)
	for name, kind := range map[string]engine.Kind{"standup": engine.KindLLM, "backup": engine.KindProgram} {
		x, err := a.Find(name)
		if err != nil || x.Kind() != kind {
			t.Fatalf("%s: %v %v", name, x, err)
		}
	}
	names := func(kinds ...engine.Kind) string {
		all, _, err := a.All(kinds...)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, x := range all {
			out = append(out, x.Name())
		}
		return strings.Join(out, " ")
	}
	if got := names(); got != "backup standup" {
		t.Fatalf("all: %s", got)
	}
	if got := names(engine.KindLLM); got != "standup" {
		t.Fatalf("llm: %s", got)
	}
	if got := names(engine.KindProgram); got != "backup" {
		t.Fatalf("program: %s", got)
	}
	if _, _, err := a.Routine("backup", "memories"); err == nil || !strings.Contains(err.Error(), "backup is a program") {
		t.Fatalf("a program as an LLM routine: %v", err)
	}
	if why := a.NameTaken("backup"); why == "" {
		t.Fatal("a program's name is free")
	}
	if why := a.NameTaken("standup"); why == "" {
		t.Fatal("a routine's name is free")
	}

	// A routine file made by hand with a program's name: neither runs.
	_ = os.WriteFile(filepath.Join(a.Layout.Routines(), "backup.prompt"), []byte("Go.\n"), 0o600)
	if _, err := a.Find("backup"); err == nil || !strings.Contains(err.Error(), "both") {
		t.Fatalf("a name both kinds have: %v", err)
	}
	all, broken, _ := a.All()
	if len(all) != 1 || len(broken) != 2 {
		t.Fatalf("all %d, broken %+v", len(all), broken)
	}
}

const succeeds = `echo "args: $*"
echo "in $(pwd), trigger $PA_TRIGGER, home $HOME, token $API_TOKEN"
echo "Copied 3 files" > "$PA_RESULT"
`

func TestProgramRunSucceeds(t *testing.T) {
	a, _ := enginetest.Fixture(t, nil, nil)
	if err := enginetest.Secrets.Set(engine.ProgramSecretKey("backup", "api-token"), "s3cret"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = enginetest.Secrets.Delete(engine.ProgramSecretKey("backup", "api-token")) })
	enginetest.AddProgram(t, a, "backup", "executable: ./prog\nargs: [--quiet]\nwrites: true\nsecrets:\n  - name: api-token\n", succeeds, true)
	res, err := run(t, a, "backup", engine.RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != engine.KindProgram || res.Program == nil || res.Program.ExitCode != 0 || res.Agent != nil {
		t.Fatalf("%+v", res)
	}
	out := res.Program.Output
	if !strings.Contains(out, "args: --quiet\n") || !strings.Contains(out, filepath.Join("program-data", "backup")+", trigger") ||
		!strings.Contains(out, "trigger manual") || !strings.Contains(out, "token s3cret") {
		t.Fatalf("output: %s", out)
	}
	run := enginetest.LastRun(a, "backup")
	if run.Outcome != runstatus.OutcomeSuccess || run.Reason != "Copied 3 files" {
		t.Fatalf("run status: %+v", run)
	}
	e := latest(t, a, "backup")
	if e.Kind != "program" || e.Program == nil || e.Program.Summary != "Copied 3 files" || e.Result != nil || e.Program.Checksum == "" {
		t.Fatalf("history: %+v", e)
	}
}

func TestProgramRunFails(t *testing.T) {
	a, _ := enginetest.Fixture(t, nil, nil)
	enginetest.AddProgram(t, a, "backup", "executable: ./prog\nwrites: false\n", "echo 'disk full' >&2\nexit 2\n", true)
	if _, err := run(t, a, "backup", engine.RunOptions{}); err == nil {
		t.Fatal("a program that exited 2 succeeded")
	}
	if r := enginetest.LastRun(a, "backup"); r.Outcome != runstatus.OutcomeFailed || !strings.Contains(r.Reason, "status 2: disk full") {
		t.Fatalf("%+v", r)
	}
	if e := latest(t, a, "backup"); e.Program == nil || e.Program.ExitCode != 2 {
		t.Fatalf("%+v", e.Program)
	}
	// A secret it declares that isn't stored is a sign-in it needs.
	enginetest.AddProgram(t, a, "needs", "executable: ./prog\nwrites: false\nsecrets:\n  - name: key\n", "true\n", true)
	if _, err := run(t, a, "needs", engine.RunOptions{}); err == nil {
		t.Fatal("ran without its secret")
	}
	if r := enginetest.LastRun(a, "needs"); r.Outcome != runstatus.OutcomeNeedsReauth {
		t.Fatalf("%+v", r)
	}
}

func TestProgramTimeout(t *testing.T) {
	a, _ := enginetest.Fixture(t, nil, nil)
	enginetest.AddProgram(t, a, "slow", "executable: ./prog\nwrites: false\ntimeout: 1s\n", "sleep 30\n", true)
	start := time.Now()
	_, err := run(t, a, "slow", engine.RunOptions{})
	if err == nil || !strings.Contains(err.Error(), "timed out after 1s (routines.slow.timeout)") {
		t.Fatalf("%v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("took %s", time.Since(start))
	}
	if r := enginetest.LastRun(a, "slow"); r.Outcome != runstatus.OutcomeFailed {
		t.Fatalf("%+v", r)
	}
}

// Stopping a program's run kills its whole process group: what it started
// ends with it, and the run is recorded as stopped.
func TestProgramStopKillsTheGroup(t *testing.T) {
	a, _ := enginetest.Fixture(t, nil, nil)
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	enginetest.AddProgram(t, a, "loop", "executable: ./prog\nwrites: false\n",
		"sh -c 'trap \"\" TERM; while :; do sleep 1; done' &\necho $! > "+pidFile+"\nwait\n", true)
	done := enginetest.RunNamed(t, a, "loop", engine.RunOptions{})
	enginetest.WaitOutcome(t, a, "loop", runstatus.OutcomeRunning)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(pidFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("it never started its child")
		}
	}
	if err := a.RequestStop("loop"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, engine.ErrStopped) {
			t.Fatalf("run returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the run didn't stop")
	}
	if r := enginetest.LastRun(a, "loop"); r.Outcome != runstatus.OutcomeStopped {
		t.Fatalf("%+v", r)
	}
	b, _ := os.ReadFile(pidFile)
	var pid int
	_, _ = fmt.Sscan(strings.TrimSpace(string(b)), &pid)
	for deadline := time.Now().Add(2 * time.Second); syscall.Kill(pid, 0) == nil; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatal("its child outlived the stop")
		}
	}
}

// Trust is keyed to the executable's checksum: a program runs only while
// it's trusted, dry runs too, and another executable takes the trust away.
func TestProgramTrust(t *testing.T) {
	a, _ := enginetest.Fixture(t, nil, nil)
	p := enginetest.AddProgram(t, a, "backup", "executable: ./prog\nwrites: false\n", "echo hi\n", false)
	for _, dry := range []bool{false, true} {
		if _, err := run(t, a, "backup", engine.RunOptions{DryRun: dry}); err == nil || !strings.Contains(err.Error(), "not trusted yet") {
			t.Fatalf("an untrusted program ran (dry run %v): %v", dry, err)
		}
	}
	x, _ := a.Find("backup")
	if err := a.Trust(x); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, a, "backup", engine.RunOptions{}); err != nil {
		t.Fatal(err)
	}
	// Its copy changed (in place of the one trusted): untrusted, and why.
	if err := os.WriteFile(p.Executable, []byte("#!/bin/sh\necho swapped\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	why, err := a.Untrusted(x)
	if err != nil || !strings.Contains(why, "has another executable") {
		t.Fatalf("%q %v", why, err)
	}
	if _, err := run(t, a, "backup", engine.RunOptions{}); err == nil {
		t.Fatal("a changed executable ran")
	}
	// Its description file is trusted too: declaring writes takes it away.
	enginetest.AddProgram(t, a, "other", "executable: ./prog\nwrites: false\n", "true\n", true)
	src := filepath.Join(t.TempDir(), programs.DescriptionFile)
	_ = os.WriteFile(filepath.Join(filepath.Dir(src), "prog"), []byte("#!/bin/sh\ntrue\n"), 0o755)
	_ = os.WriteFile(src, []byte("executable: ./prog\nwrites: true\n"), 0o600)
	if _, err := programs.Add(a.Layout.Programs(), "other", src, true); err != nil {
		t.Fatal(err)
	}
	y, _ := a.Find("other")
	if why, _ := a.Untrusted(y); !strings.Contains(why, "now writes") {
		t.Fatalf("%q", why)
	}
}

// What runs is the app's copy: changing the file it was added from changes
// nothing.
func TestProgramRunsItsCopy(t *testing.T) {
	a, _ := enginetest.Fixture(t, nil, nil)
	src := t.TempDir()
	_ = os.WriteFile(filepath.Join(src, "prog"), []byte("#!/bin/sh\necho original\n"), 0o755)
	_ = os.WriteFile(filepath.Join(src, programs.DescriptionFile), []byte("executable: ./prog\nwrites: false\n"), 0o600)
	_ = a.InitTrust()
	if _, err := programs.Add(a.Layout.Programs(), "hello", filepath.Join(src, programs.DescriptionFile), false); err != nil {
		t.Fatal(err)
	}
	x, _ := a.Find("hello")
	_ = a.Trust(x)
	_ = os.WriteFile(filepath.Join(src, "prog"), []byte("#!/bin/sh\necho swapped\n"), 0o755)
	res, err := run(t, a, "hello", engine.RunOptions{})
	if err != nil || strings.TrimSpace(res.Program.Output) != "original" {
		t.Fatalf("%+v %v", res, err)
	}
}

// A program doesn't wait in the run queue: it runs while a queued LLM
// routine holds it.
func TestProgramSkipsTheQueue(t *testing.T) {
	gate := make(chan struct{})
	a, set := enginetest.Fixture(t, map[string]string{"first": "queue"}, map[string]chan struct{}{"first": gate})
	enginetest.AddProgram(t, a, "quick", "executable: ./prog\nwrites: false\n", "echo done\n", true)
	first := enginetest.Start(t, a, set, "first")
	enginetest.WaitOutcome(t, a, "first", runstatus.OutcomeRunning)
	select {
	case err := <-enginetest.RunNamed(t, a, "quick", engine.RunOptions{}):
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a program waited in the run queue")
	}
	if r := enginetest.LastRun(a, "quick"); r.QueuedAt != nil || r.Outcome != runstatus.OutcomeSuccess {
		t.Fatalf("%+v", r)
	}
	close(gate)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
}

// A dry run passes --dry-run to a program that writes, and runs one that
// doesn't as it is; neither leaves a record.
func TestProgramDryRun(t *testing.T) {
	a, _ := enginetest.Fixture(t, nil, nil)
	enginetest.AddProgram(t, a, "writer", "executable: ./prog\nargs: [--all]\nwrites: true\n", "echo \"args: $*\"\n", true)
	enginetest.AddProgram(t, a, "reader", "executable: ./prog\nargs: [--all]\nwrites: false\n", "echo \"args: $*\"\n", true)
	for name, want := range map[string]string{"writer": "args: --all --dry-run", "reader": "args: --all"} {
		res, err := run(t, a, name, engine.RunOptions{DryRun: true})
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(res.Program.Output); got != want || !res.Program.DryRun {
			t.Fatalf("%s: %q", name, got)
		}
		if runs, _ := history.List(a.Layout.History(), history.Filter{Routine: name}); len(runs) != 0 {
			t.Fatalf("%s: a dry run left a record", name)
		}
	}
}

// Kind absent means llm: an LLM routine's record is written as before, with
// no kind.
func TestLLMRecordsHaveNoKind(t *testing.T) {
	a, set := enginetest.Fixture(t, map[string]string{"standup": "queue"}, nil)
	if err := <-enginetest.Start(t, a, set, "standup"); err != nil {
		t.Fatal(err)
	}
	e := latest(t, a, "standup")
	path := filepath.Join(a.Layout.History(), e.ID+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	_ = json.Unmarshal(b, &raw)
	if _, ok := raw["kind"]; ok || raw["result"] == nil {
		t.Fatalf("an LLM routine's record: %s", b)
	}
	if k, _ := engine.ParseKind(e.Kind); k != engine.KindLLM {
		t.Fatalf("read as %s", k)
	}
}

// What a program writes to $PA_NOTIFY becomes the app's notifications about
// its run: up to 20, a bad line reported (not failing the run); a dry run
// records none.
func TestProgramNotifies(t *testing.T) {
	a, _ := enginetest.Fixture(t, nil, nil)
	script := `echo '{"title":"Review requested","body":"acme/app#7","url":"https://github.com/acme/app/pull/7"}' >> "$PA_NOTIFY"
echo '{"title":"Fix the build","body":"main is red","acknowledge":true}' >> "$PA_NOTIFY"
echo '{"title":"bad link","url":"file:///etc/passwd"}' >> "$PA_NOTIFY"
echo 'not json' >> "$PA_NOTIFY"
echo '{"title":"to ack, no body","acknowledge":true}' >> "$PA_NOTIFY"
i=0; while [ $i -lt 20 ]; do echo '{"title":"more"}' >> "$PA_NOTIFY"; i=$((i+1)); done
`
	enginetest.AddProgram(t, a, "gh", "executable: ./prog\nwrites: false\n", script, true)
	res, err := run(t, a, "gh", engine.RunOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if p := res.Program; len(p.Notified) != 20 || p.Notified[0] != "would notify: Review requested" {
		t.Fatalf("dry run: %+v", p.Notified)
	}
	if f, _ := notifications.Load(a.Layout.Notifications()); len(f.Notifications) != 0 {
		t.Fatal("a dry run notified")
	}
	if res, err = run(t, a, "gh", engine.RunOptions{}); err != nil {
		t.Fatal(err)
	}
	p := res.Program
	if len(p.Notified) != 20 || len(p.NotifyProblems) != 4 || !strings.Contains(p.NotifyProblems[3], "2 more notifications dropped") ||
		!strings.Contains(p.NotifyProblems[0], "line 3") || !strings.Contains(p.NotifyProblems[2], "body is required") {
		t.Fatalf("notified %v, problems %v", p.Notified, p.NotifyProblems)
	}
	f, _ := notifications.Load(a.Layout.Notifications())
	first, ack := f.Notifications[0], f.Notifications[1]
	if len(f.Notifications) != 20 || first.Kind != notifications.KindRun || first.URL != "https://github.com/acme/app/pull/7" ||
		first.Routine != "gh" || first.Run == "" || first.Hook || !ack.Waiting() {
		t.Fatalf("%+v", f.Notifications[:2])
	}
	if e := latest(t, a, "gh"); len(e.Program.Notified) != 20 || len(e.Program.NotifyProblems) != 4 {
		t.Fatalf("history: %+v", e.Program)
	}
	if s, _ := runstatus.Load(a.Layout.RunStatus()); s.PendingAcknowledgements != 1 {
		t.Fatalf("waiting: %+v", s.Waiting)
	}
}
