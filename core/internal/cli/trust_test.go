package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/engine/enginetest"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
)

func TestTrust(t *testing.T) {
	// "old" exists before trust is first used: it keeps running.
	a, _ := enginetest.Fixture(t, map[string]string{"old": "queue"}, nil)
	cfg := a.Layout.Config()
	withNotesServer(t, cfg)
	src := func(tools string) string {
		return "---\nautomator.servers: [notes]\nautomator.schedule: \"0 9 * * *\"\ntools: [" + tools + "]\n---\nGo. {{> memories}}\n"
	}
	if _, err := cli(t, cfg, "routines", "create", "fresh", "--source", src("list_notes")); err != nil {
		t.Fatal(err)
	}
	reload := func() (*engine.App, *routines.Set) {
		a, err := loadTest(cfg, nil)
		if err != nil {
			t.Fatal(err)
		}
		set, _ := routines.Load(a.Layout.Routines())
		return a, set
	}
	run := func(name string, dry bool) error {
		a, set := reload()
		r, _ := set.Find(name)
		_, err := a.Execute(context.Background(), set, r, engine.RunOptions{DryRun: dry})
		return err
	}
	if err := run("old", false); err != nil {
		t.Fatalf("a routine from before trust existed was stopped: %v", err)
	}
	if err := run("fresh", false); err == nil || !strings.Contains(err.Error(), "not trusted yet") {
		t.Fatalf("an untrusted routine ran for real: %v", err)
	}
	if err := run("fresh", true); err != nil && strings.Contains(err.Error(), "trusted") {
		t.Fatalf("a dry run needed trust: %v", err)
	}
	// Untrusted and enabled: not scheduled, with the reason in run status.
	_, _ = cli(t, cfg, "settings", "set", "routines.fresh.enabled", "true")
	a, set := reload()
	fresh, _ := set.Find("fresh")
	if rd := a.Assess(a.LLM(fresh, set)); !rd.Config.Enabled || !strings.Contains(rd.Untrusted, "routines trust fresh") {
		t.Fatalf("readiness: %+v", rd)
	}
	// Trusting it (a review in a terminal, or --yes).
	if out, err := cli(t, cfg, "routines", "trust", "fresh", "--yes"); err != nil || !strings.Contains(out, "Trusted fresh") {
		t.Fatalf("trust: %s %v", out, err)
	}
	if err := run("fresh", false); err != nil && strings.Contains(err.Error(), "trusted") {
		t.Fatalf("trusted routine refused: %v", err)
	}
	// Its prompt can change; what it can reach can't.
	if _, err := cli(t, cfg, "routines", "update", "fresh", "--source", strings.Replace(src("list_notes"), "Go.", "Go now.", 1)); err != nil {
		t.Fatal(err)
	}
	if err := run("fresh", false); err != nil && strings.Contains(err.Error(), "trusted") {
		t.Fatalf("a prompt edit took the trust away: %v", err)
	}
	if _, err := cli(t, cfg, "routines", "update", "fresh", "--source", src("list_notes, notify_me")); err != nil {
		t.Fatal(err)
	}
	if err := run("fresh", false); err == nil || !strings.Contains(err.Error(), "adds tool notify_me") {
		t.Fatalf("a new tool kept the trust: %v", err)
	}
	out, _ := cli(t, cfg, "routines", "list", "--json")
	if !strings.Contains(out, `"untrusted": "changed since you trusted it (adds tool notify_me)`) {
		t.Fatalf("routines list: %s", out)
	}
	// Deleting forgets it: the same name made again isn't trusted.
	_, _ = cli(t, cfg, "routines", "trust", "fresh", "--yes")
	_, _ = cli(t, cfg, "routines", "delete", "fresh")
	_, _ = cli(t, cfg, "routines", "create", "fresh", "--source", src("list_notes"))
	if err := run("fresh", false); err == nil || !strings.Contains(err.Error(), "not trusted yet") {
		t.Fatalf("a re-created routine inherited trust: %v", err)
	}
	// Without a terminal, trusting needs --yes (and isn't an MCP tool at all).
	if _, err := cli(t, cfg, "routines", "trust", "fresh"); err == nil {
		t.Fatal("trusted without asking")
	}
	if _, ok := toolMap(testServer(t, false))["routines_trust"]; ok {
		t.Fatal("trusting is served over MCP")
	}
}

func TestDoctor(t *testing.T) {
	a, _ := enginetest.Fixture(t, map[string]string{"ok": "queue"}, nil)
	cfg := a.Layout.Config()
	withNotesServer(t, cfg)
	_, _ = cli(t, cfg, "routines", "create", "new", "--source", "---\nautomator.servers: [notes]\ntools: [list_notes]\n---\nGo.")
	_, _ = cli(t, cfg, "settings", "set", "routines.new.enabled", "true")
	out, err := cli(t, cfg, "doctor", "--json")
	if err == nil {
		t.Fatal("doctor passed with problems")
	}
	for _, want := range []string{`"area": "scheduler"`, `"subject": "new"`, `"fix": "procauto routines trust new"`} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor lacks %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, `"subject": "ok"`) {
		t.Errorf("a fine routine was reported:\n%s", out)
	}
}

func TestTrustCheckAndPause(t *testing.T) {
	a, _ := enginetest.Fixture(t, map[string]string{"old": "queue"}, nil)
	cfg := a.Layout.Config()
	// --check reviews a trusted routine too (frontends show what it reaches), trusting nothing new.
	out, err := cli(t, cfg, "routines", "trust", "old", "--check")
	if err != nil || strings.Contains(out, "already trusted") || !strings.Contains(out, "old can reach") {
		t.Fatalf("check of a trusted routine: %s %v", out, err)
	}
	if _, err := cli(t, cfg, "routines", "trust", "old", "--check", "--yes"); err == nil {
		t.Error("--check with --yes was accepted")
	}
	// pause and resume set its enabled setting.
	for _, tc := range []struct{ cmd, want string }{{"pause", "false"}, {"resume", "true"}} {
		if _, err := cli(t, cfg, "routines", tc.cmd, "old"); err != nil {
			t.Fatal(err)
		}
		if got, _ := cli(t, cfg, "settings", "get", "routines.old.enabled"); !strings.HasSuffix(strings.TrimSpace(got), "= "+tc.want) {
			t.Errorf("after %s: enabled %q", tc.cmd, got)
		}
	}
	if _, err := cli(t, cfg, "routines", "pause", "nope"); err == nil {
		t.Error("paused a routine that doesn't exist")
	}
}
