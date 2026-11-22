package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The scheduler's unit is looked for where systemd's user manager reads
// units (its XDG_CONFIG_HOME), not where this process's XDG_CONFIG_HOME
// points; the tray's autostart entry in this session's.
func TestLoginItemsFindTheUnitWhereSystemdReadsIt(t *testing.T) {
	session, manager, bin := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", session)
	systemctl := "#!/bin/sh\ncase \"$2\" in\n" +
		"show-environment) printf 'HOME=/x\\nXDG_CONFIG_HOME=%s\\n' '" + manager + "' ;;\n" +
		"is-enabled) echo enabled ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "systemctl"), []byte(systemctl), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	labels := loginItemLabels()
	write := func(path string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("[Desktop Entry]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(manager, "systemd", "user", labels["scheduler"]))
	write(filepath.Join(session, "autostart", labels["ui"]))
	items, err := loginItems(labels)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if !it.Installed || !it.Enabled {
			t.Errorf("%s: %+v", it.Name, it)
		}
	}
}

// fakeSystemctl stands in for systemctl --user: the user manager is there
// unless nosession exists, the unit is active while active exists.
func fakeSystemctl(t *testing.T) (string, func() []string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	return fakeTool(t, "systemctl", `[ -f "$d/nosession" ] && { echo "Failed to connect to bus" >&2; exit 1; }
case $2 in
show-environment) printf "HOME=/x\\nXDG_CONFIG_HOME=%s\\n" "$XDG_CONFIG_HOME" ;;
is-active) if [ -f "$d/active" ]; then echo active; else echo inactive; exit 3; fi ;;
start) : > "$d/active" ;;
stop) rm -f "$d/active" ;;
esac`)
}

// start starts the unit when it isn't active, and says what to do without
// a user session or a unit.
func TestStartStartsTheUnitOnce(t *testing.T) {
	dir, calls := fakeSystemctl(t)
	labels := loginItemLabels()
	if _, err := startLoginItems(labels); err == nil || !strings.Contains(err.Error(), "install") {
		t.Fatalf("not installed: %v", err)
	}
	unit := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "systemd", "user", labels["scheduler"])
	if err := os.MkdirAll(filepath.Dir(unit), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := startLoginItems(labels)
	if err != nil || !out.Changed || out.Scheduler != "running" || !hasCall(calls(), "systemctl --user start "+labels["scheduler"]) {
		t.Fatal(out, err, calls())
	}
	_ = os.Remove(filepath.Join(dir, "log"))
	out, err = startLoginItems(labels)
	if err != nil || out.Changed || hasCall(calls(), "systemctl --user start") {
		t.Fatal(out, err, calls())
	}
	_ = os.Remove(filepath.Join(dir, "active"))
	if err := os.WriteFile(filepath.Join(dir, "nosession"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := startLoginItems(labels); err == nil || !strings.Contains(err.Error(), "daemon") {
		t.Fatal(err)
	}
}

// stop stops the unit, then the tray and its windows running from the
// app's folder, but not the process that asked.
func TestStopStopsTheUnitAndTheTray(t *testing.T) {
	dir, calls := fakeSystemctl(t)
	labels := loginItemLabels()
	if err := os.WriteFile(filepath.Join(dir, "active"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	tray, exited := startSleeper(t)
	frontendExecutables = []string{filepath.Base(tray.Path), "automator-windows"}
	t.Cleanup(func() { frontendExecutables = []string{"automator-tray", "automator-windows"} })
	// The tray that asked is spared.
	out, err := stopLoginItems(labels, filepath.Dir(tray.Path), []int{os.Getpid(), tray.Process.Pid})
	if err != nil || !out.Changed || out.Scheduler != "stopped" || out.UIStopped || !hasCall(calls(), "systemctl --user stop "+labels["scheduler"]) {
		t.Fatal(out, err, calls())
	}
	out, err = stopLoginItems(labels, filepath.Dir(tray.Path), []int{os.Getpid()})
	if err != nil || out.Changed || !out.UIStopped || !exited() {
		t.Fatal(out, err)
	}
}
