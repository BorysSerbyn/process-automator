package engine

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/notifications"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/update"
)

// The scheduler's pass starts an automatic install of the newer release as
// soon as nothing is running, once (it records the try), and not while a run is going,
// with automatic updates off, or on a build that can't be updated. Starting
// it failing is a notification; the reason it waits is recorded for
// `update status`.
func TestAutoUpdatePass(t *testing.T) {
	a := testApp(t)
	t.Setenv("INVOCATION_ID", "") // never a systemd unit, even under one
	origAuto, origBlocked, origCmd, origVersion := autoUpdates, autoBlocked, updateCommand, product.Version
	t.Cleanup(func() {
		autoUpdates, autoBlocked, updateCommand, product.Version = origAuto, origBlocked, origCmd, origVersion
	})
	autoUpdates, product.Version = true, "1.0.0"
	blocked := ""
	autoBlocked = func() string { return blocked }
	var started [][]string
	startErr := error(nil)
	updateCommand = func(_ *App, env []string) (*exec.Cmd, error) {
		started = append(started, env)
		if startErr != nil {
			return nil, startErr
		}
		return exec.Command("true"), nil
	}
	now := time.Now()
	// Checked a minute ago: no check now (tests never reach GitHub).
	if err := update.RecordCheck(a.Layout.UpdateAuto(), "v2.0.0", true, nil, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	u := newAutoUpdater()
	state := func() update.AutoState {
		t.Helper()
		st, err := update.LoadAuto(a.Layout.UpdateAuto())
		if err != nil {
			t.Fatal(err)
		}
		return st
	}

	// A routine running: it waits, and says why.
	running := runstatus.Run{StartedAt: now, Outcome: runstatus.OutcomeRunning}
	if err := runstatus.Record(a.Layout.RunStatus(), "standup", running, now); err != nil {
		t.Fatal(err)
	}
	u.pass(context.Background(), a, now)
	if len(started) != 0 || !strings.Contains(state().Waiting, "standup is running") {
		t.Fatalf("while running: started %v, %+v", started, state())
	}
	st, err := a.UpdateStatus(now)
	if err != nil || !st.Automatic || !st.UpdateAvailable || !strings.Contains(st.Waiting, "standup is running") || st.Setting != AutoUpdateSetting {
		t.Fatalf("update status: %+v, %v", st, err)
	}
	finished := now
	running.Outcome, running.FinishedAt = runstatus.OutcomeSuccess, &finished
	if err := runstatus.Record(a.Layout.RunStatus(), "standup", running, now); err != nil {
		t.Fatal(err)
	}

	// Off, or a build that can't be updated: nothing.
	store := settings.Store{Path: a.Layout.Config()}
	if _, err := store.Set(AutoUpdateSetting, "false"); err != nil {
		t.Fatal(err)
	}
	u.pass(context.Background(), a, now)
	if st, _ := a.UpdateStatus(now); len(started) != 0 || st.Automatic || st.Waiting != "" {
		t.Fatalf("off: started %v, %+v", started, st)
	}
	if _, err := store.Reset(AutoUpdateSetting); err != nil {
		t.Fatal(err)
	}
	blocked = "a development build"
	u.pass(context.Background(), a, now)
	if len(started) != 0 {
		t.Fatalf("blocked: started %v", started)
	}
	blocked = ""

	// Nothing running: it starts at once (the release was found a minute
	// ago), once, marked automatic.
	u.pass(context.Background(), a, now)
	u.pass(context.Background(), a, now.Add(time.Minute))
	if len(started) != 1 || !strings.Contains(strings.Join(started[0], " "), AutoUpdateEnv+"=1") || state().Tried != "v2.0.0" {
		t.Fatalf("quiet: started %v, %+v", started, state())
	}
	if st := state(); !strings.Contains(st.Waiting, "didn't finish") {
		t.Fatalf("after a try: %+v", st)
	}

	// A day later it's tried again; when it can't start, a notification says so.
	startErr = errors.New("no executable")
	u.pass(context.Background(), a, now.Add(25*time.Hour))
	f, err := notifications.Load(a.Layout.Notifications())
	if err != nil {
		t.Fatal(err)
	}
	list := f.Notifications
	if len(started) != 2 || len(list) != 1 || !list[0].Update || !strings.Contains(list[0].Title, "v2.0.0") ||
		!strings.Contains(list[0].Body, "keeps working") {
		t.Fatalf("couldn't start: started %v, %+v", started, list)
	}
}

// What's running, from run status: a run stuck past its deadline isn't, and
// one waiting its turn hasn't started.
func TestRunning(t *testing.T) {
	now := time.Now()
	old := now.Add(-time.Hour)
	s := &runstatus.Status{
		Routines: []runstatus.Routine{
			{Name: "a", LastRun: &runstatus.Run{StartedAt: old, Deadline: &old, Outcome: runstatus.OutcomeRunning}},
			{Name: "b", LastRun: &runstatus.Run{StartedAt: now, Outcome: runstatus.OutcomeQueued}},
		},
	}
	if busy := running(s, now); busy != "" {
		t.Fatalf("stuck or queued: %q", busy)
	}
	s.Hooks = []runstatus.Routine{{Name: "h", LastRun: &runstatus.Run{StartedAt: now, Outcome: runstatus.OutcomeRunning}}}
	if busy := running(s, now); busy != "h is running" {
		t.Fatalf("a hook running: %q", busy)
	}
}
