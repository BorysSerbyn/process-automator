//go:build unix

package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/BorysSerbyn/process-automator/core/internal/engine/enginetest"
	"github.com/BorysSerbyn/process-automator/core/internal/hooks"
	"github.com/BorysSerbyn/process-automator/core/internal/watchers"
)

// Turning a hook on is one review and one yes: the review shows what its
// watcher runs and the sign-ins still needed, and trusting the hook trusts
// its watcher too.
func TestTurningAHookOnTrustsItsWatcher(t *testing.T) {
	a, _ := enginetest.Fixture(t, map[string]string{"standup": "queue"}, nil)
	cfg := a.Layout.Config()
	watcher := hookFiles(t, watchers.DescriptionFile, "description: Calendar\nexecutable: ./watch\nmode: poll\ninterval: 30s\n"+
		"args: [--soon]\nsecrets:\n  - name: turnon-cal-token\n", "watch", "true\n")
	program := hookFiles(t, hooks.DescriptionFile, "executable: ./act\nwrites: false\non: {watcher: turnon-cal, type: meeting.started}\n",
		"act", "cat\n")
	cliJSON[WatcherChange](t, cfg, "watchers", "add", "turnon-cal", "--from", watcher)
	cliJSON[HookChange](t, cfg, "hooks", "add", "turnon-meet", "--from", program)

	list := cliJSON[[]HookInfo](t, cfg, "hooks", "list")
	if len(list) != 1 || !list[0].TurnOn {
		t.Fatalf("an off, untrusted hook doesn't need turning on: %+v", list)
	}
	review := cliJSON[TrustOutput](t, cfg, "hooks", "trust", "turnon-meet", "--check")
	w := review.Watcher
	if w == nil || w.Trusted || w.Untrusted != "not trusted yet" || w.Mode != "poll" || w.Description != "Calendar" ||
		w.Program == nil || w.Program.Checksum == "" || strings.Join(w.Program.Args, " ") != "--soon" ||
		len(w.Program.Secrets) != 1 || w.Program.Secrets[0].Stored {
		t.Fatalf("the review lacks what its watcher runs: %+v %+v", w, w.Program)
	}
	if !slices.Equal(review.SignIns, []string{"turnon-cal"}) {
		t.Fatalf("sign-ins: %v", review.SignIns)
	}
	if out, _ := cli(t, cfg, "hooks", "trust", "turnon-meet", "--check"); !strings.Contains(out, "trusting the hook trusts it too") ||
		!strings.Contains(out, "auth login turnon-cal") {
		t.Fatalf("the review in words: %s", out)
	}

	on := cliJSON[TrustOutput](t, cfg, "hooks", "trust", "turnon-meet", "--yes", "--resume")
	if !on.Trusted || !on.Resumed || on.Watcher == nil || !on.Watcher.Trusted {
		t.Fatalf("turning it on: %+v %+v", on, on.Watcher)
	}
	if got := cliJSON[WatcherDetail](t, cfg, "watchers", "show", "turnon-cal"); !got.Trusted {
		t.Fatal("its watcher wasn't trusted with it")
	}
	if got := cliJSON[[]HookInfo](t, cfg, "hooks", "list"); got[0].TurnOn || strings.Contains(got[0].Problem, "trusted") {
		t.Fatalf("still needs turning on: %+v", got[0])
	}

	// A watcher that changed since: the hook is trusted, and trusting it
	// again (no --resume) trusts the watcher, rather than saying it's done.
	if _, err := cli(t, cfg, "watchers", "update", "turnon-cal", "--from", hookFiles(t, watchers.DescriptionFile,
		"executable: ./watch\nmode: poll\n", "watch", "echo changed\n")); err != nil {
		t.Fatal(err)
	}
	if got := cliJSON[[]HookInfo](t, cfg, "hooks", "list"); !got[0].TurnOn {
		t.Fatal("a hook whose watcher changed doesn't need turning on")
	}
	changed := cliJSON[TrustOutput](t, cfg, "hooks", "trust", "turnon-meet", "--check")
	if changed.Watcher.Trusted || len(changed.Watcher.Changes) == 0 || changed.Watcher.Untrusted != "changed since it was trusted" {
		t.Fatalf("a changed watcher: %+v", changed.Watcher)
	}
	if again := cliJSON[TrustOutput](t, cfg, "hooks", "trust", "turnon-meet", "--yes"); !again.Watcher.Trusted {
		t.Fatalf("trusting a trusted hook left its watcher untrusted: %+v", again.Watcher)
	}

	// No such watcher: the hook is trusted, the review says why it hears nothing.
	missing := hookFiles(t, "lonely.prompt", "---\nautomator.on: {watcher: nowhere}\n---\nHi\n", "", "")
	cliJSON[HookChange](t, cfg, "hooks", "add", "turnon-lonely", "--from", missing)
	lonely := cliJSON[TrustOutput](t, cfg, "hooks", "trust", "turnon-lonely", "--yes", "--resume")
	if !lonely.Trusted || lonely.Watcher == nil || lonely.Watcher.Trusted || lonely.Watcher.Program != nil {
		t.Fatalf("a hook with no watcher: %+v %+v", lonely, lonely.Watcher)
	}
}

// A routine needs turning on while it isn't trusted or is paused with a
// schedule; a program's review names the sign-in it still needs.
func TestTurningRoutinesOn(t *testing.T) {
	a, _ := enginetest.Fixture(t, map[string]string{"standup": "queue"}, nil)
	cfg := a.Layout.Config()
	desc := programSource(t, "executable: ./prog\nwrites: false\nschedule: \"0 9 * * *\"\nsecrets:\n  - name: turnon-api\n", "true\n")
	cliJSON[RoutineChange](t, cfg, "routines", "create", "turnon-backup", "--kind", "program", "--from", desc)
	row := func() RoutineInfo {
		for _, r := range cliJSON[[]RoutineInfo](t, cfg, "routines", "list", "--kind", "all") {
			if r.Name == "turnon-backup" {
				return r
			}
		}
		t.Fatal("not listed")
		return RoutineInfo{}
	}
	if !row().TurnOn {
		t.Fatal("a paused, untrusted program doesn't need turning on")
	}
	review := cliJSON[TrustOutput](t, cfg, "routines", "trust", "turnon-backup", "--check")
	if !slices.Equal(review.SignIns, []string{"turnon-backup"}) || review.Watcher != nil {
		t.Fatalf("%+v", review)
	}
	cliJSON[TrustOutput](t, cfg, "routines", "trust", "turnon-backup", "--yes")
	if !row().TurnOn {
		t.Fatal("a trusted program paused with a schedule doesn't need turning on")
	}
	if on := cliJSON[TrustOutput](t, cfg, "routines", "trust", "turnon-backup", "--yes", "--resume"); !on.Resumed {
		t.Fatalf("%+v", on)
	}
	if row().TurnOn {
		t.Fatal("a trusted program on its schedule still needs turning on")
	}
}
