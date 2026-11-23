//go:build unix

package engine

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/hooks"
	"github.com/BorysSerbyn/process-automator/core/internal/permissions"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
)

// pidWatcher is a stream watcher that writes its process ID to pid in its
// folder and keeps running.
const pidWatcher = "echo $$ > pid.tmp && mv pid.tmp pid\nexec sleep 60\n"

// watcherPID is the running watcher's process ID (0: none has started
// since the file was last removed, or it has stopped).
func watcherPID(a *App, name string) int {
	b, err := os.ReadFile(filepath.Join(a.Layout.WatcherData(name), "pid"))
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	if pid == 0 || syscall.Kill(pid, 0) != nil {
		return 0
	}
	return pid
}

// statusHook is hook as run status has it (nil: not there).
func statusHook(a *App, hook string) *runstatus.Routine {
	s, err := runstatus.Load(a.Layout.RunStatus())
	if err != nil || s == nil {
		return nil
	}
	for i := range s.Hooks {
		if s.Hooks[i].Name == hook {
			return &s.Hooks[i]
		}
	}
	return nil
}

// startDaemon runs the daemon until the test ends, and waits for its first
// pass to finish (run status lists hook).
func startDaemon(t *testing.T, a *App, hook string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Daemon(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	waitUntil(t, "the daemon's first pass", func() bool {
		return statusHook(a, hook) != nil
	})
	time.Sleep(300 * time.Millisecond) // the rest of the pass: the watchers it starts
}

// setEnabled turns a hook on or off as `hooks resume` and `hooks pause` do,
// with what follows a command that changes something.
func setEnabled(t *testing.T, a *App, hook string, on bool) time.Time {
	t.Helper()
	x, err := a.FindHook(hook)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (settings.Store{Path: a.Layout.Config(), Extra: x.Settings()}).Set("hooks."+hook+".enabled", strconv.FormatBool(on)); err != nil {
		t.Fatal(err)
	}
	if err := a.Changed(time.Now()); err != nil {
		t.Fatal(err)
	}
	return time.Now()
}

// within waits up to a second and a half for cond, failing past it: what a
// change woke the daemon for happens about at once, not on its next pass
// (TickInterval).
func within(t *testing.T, what string, since time.Time, cond func() bool) {
	t.Helper()
	for time.Since(since) < 1500*time.Millisecond {
		if cond() {
			t.Logf("%s after %s", what, time.Since(since).Round(time.Millisecond))
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s: not within 1.5s of the change (the next pass is %s away)", what, TickInterval)
}

// Turning a hook on starts its watcher within about a second of the
// command, and pausing it stops the watcher as fast: the command wakes the
// daemon, which makes a pass at once. Run status says so straight away.
func TestTurningAHookOnTakesEffectAtOnce(t *testing.T) {
	a := testApp(t)
	addWatcher(t, a, "keys", "executable: ./watch\nmode: stream\n", pidWatcher)
	addHook(t, a, "on-key", hooks.DescriptionFile, "executable: ./act\nwrites: false\non: {watcher: keys}\n", "true\n")
	setEnabled(t, a, "on-key", false)
	startDaemon(t, a, "on-key")
	if watcherPID(a, "keys") != 0 {
		t.Fatal("the watcher of a hook that's off runs")
	}

	at := setEnabled(t, a, "on-key", true)
	if h := statusHook(a, "on-key"); h == nil || !h.Enabled {
		t.Fatal("run status doesn't say it's on")
	}
	within(t, "the watcher started", at, func() bool { return watcherPID(a, "keys") != 0 })
	pid := watcherPID(a, "keys")

	at = setEnabled(t, a, "on-key", false)
	if h := statusHook(a, "on-key"); h == nil || h.Enabled {
		t.Fatal("run status doesn't say it's off")
	}
	within(t, "the watcher stopped", at, func() bool { return syscall.Kill(pid, 0) != nil })
	if _, err := os.Stat(a.Layout.WakeRequest()); !os.IsNotExist(err) {
		t.Fatalf("the wake file is left: %v", err)
	}
}

// A permission turned on in the OS's settings after a request (one the OS
// can't be asked for, say) is acted on at once: the daemon follows it, and
// the watcher that needs it starts.
func TestAGrantedPermissionTakesEffectAtOnce(t *testing.T) {
	fake := fakePermissions(t, map[string]permissions.Status{"full-disk-access": permissions.Missing})
	poll := followPoll
	followPoll = 50 * time.Millisecond
	t.Cleanup(func() { followPoll = poll })
	a := testApp(t)
	addWatcher(t, a, "mail", "executable: ./watch\nmode: stream\npermissions: [full-disk-access]\n", pidWatcher)
	addHook(t, a, "on-mail", hooks.DescriptionFile, "executable: ./act\nwrites: false\non: {watcher: mail}\n", "true\n")
	startDaemon(t, a, "on-mail")
	if watcherPID(a, "mail") != 0 {
		t.Fatal("a watcher runs without the permission it needs")
	}
	out, err := a.RequestPermission(context.Background(), "full-disk-access", time.Now())
	if err != nil || out.Asked {
		t.Fatalf("%+v %v", out, err)
	}
	fake.set("full-disk-access", permissions.Granted)
	within(t, "the watcher started", time.Now(), func() bool { return watcherPID(a, "mail") != 0 })
	if got := a.PermissionState().Of("full-disk-access"); got != permissions.Granted {
		t.Fatalf("recorded %s", got)
	}
}
