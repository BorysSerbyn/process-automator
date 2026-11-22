package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// launchctl print-disabled says "=> disabled/enabled" (current macOS) or
// "=> true/false" (older).
func TestDisabledLine(t *testing.T) {
	out := `disabled services = {
	"dev.x.procauto.ui" => disabled
	"dev.x.procauto.scheduler" => enabled
	"com.old.style" => true
	"com.old.other" => false
}`
	got := map[string]string{}
	for _, m := range disabledLine.FindAllStringSubmatch(out, -1) {
		got[m[1]] = m[2]
	}
	if got["dev.x.procauto.ui"] != "disabled" || got["dev.x.procauto.scheduler"] != "enabled" || got["com.old.style"] != "true" || len(got) != 4 {
		t.Fatalf("parsed %v", got)
	}
}

// fakeLaunchctl stands in for launchctl: an agent is loaded while
// loaded.<label> exists (holding its pid, if it runs); bootstrap fails
// while fail.<label> counts down; print-disabled prints the file disabled.
func fakeLaunchctl(t *testing.T) (string, func() []string) {
	t.Helper()
	agentExitWait, bootstrapRetry = 2*time.Second, 10*time.Millisecond
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "Library", "LaunchAgents"), 0o755); err != nil {
		t.Fatal(err)
	}
	return fakeTool(t, "launchctl", `case $1 in
print) l=${2##*/}; [ -f "$d/loaded.$l" ] || exit 113; echo "state = running"
  [ -s "$d/loaded.$l" ] && printf '\tpid = %s\n' "$(cat "$d/loaded.$l")"; exit 0 ;;
bootout) l=${2##*/}; [ -f "$d/loaded.$l" ] || exit 3; rm -f "$d/loaded.$l" ;;
bootstrap) l=$(basename "$3" .plist)
  if [ -s "$d/fail.$l" ] && [ "$(cat "$d/fail.$l")" -gt 0 ]; then
    echo $(( $(cat "$d/fail.$l") - 1 )) > "$d/fail.$l"; echo "Bootstrap failed: 5: Input/output error" >&2; exit 5
  fi
  : > "$d/loaded.$l" ;;
print-disabled) cat "$d/disabled" 2>/dev/null ;;
esac`)
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// start loads the scheduler's agent when it isn't loaded, and leaves it be
// when it is.
func TestStartLoadsTheSchedulerOnce(t *testing.T) {
	dir, calls := fakeLaunchctl(t)
	labels := loginItemLabels()
	home, _ := os.UserHomeDir()
	plist := filepath.Join(home, "Library", "LaunchAgents", labels["scheduler"]+".plist")
	if _, err := startLoginItems(labels); err == nil || !strings.Contains(err.Error(), "install") {
		t.Fatalf("not installed: %v", err)
	}
	writeFile(t, plist, "<plist/>")
	out, err := startLoginItems(labels)
	if err != nil || !out.Changed || out.Scheduler != "running" {
		t.Fatal(out, err)
	}
	if !hasCall(calls(), "launchctl bootstrap "+launchdDomain()+" "+plist) {
		t.Fatalf("%q", calls())
	}
	_ = os.Remove(filepath.Join(dir, "log"))
	out, err = startLoginItems(labels)
	if err != nil || out.Changed || hasCall(calls(), "launchctl bootstrap") {
		t.Fatal(out, err, calls())
	}
}

// An agent turned off at login is turned on for the load and off again; a
// load refused while launchd lets go of the agent is tried again.
func TestStartLoadsAnAgentTurnedOffAndRetries(t *testing.T) {
	dir, calls := fakeLaunchctl(t)
	labels := loginItemLabels()
	home, _ := os.UserHomeDir()
	writeFile(t, filepath.Join(home, "Library", "LaunchAgents", labels["scheduler"]+".plist"), "<plist/>")
	writeFile(t, filepath.Join(dir, "disabled"), `disabled services = {
	"`+labels["scheduler"]+`" => disabled
}`)
	writeFile(t, filepath.Join(dir, "fail."+labels["scheduler"]), "2")
	if _, err := startLoginItems(labels); err != nil {
		t.Fatal(err)
	}
	var verbs []string
	for _, c := range calls() {
		if f := strings.Fields(c); len(f) > 1 && f[1] != "print" && f[1] != "print-disabled" {
			verbs = append(verbs, f[1])
		}
	}
	if strings.Join(verbs, " ") != "enable bootstrap bootstrap bootstrap disable" {
		t.Fatalf("%q", calls())
	}
	// Refused every time: the last error.
	_ = os.Remove(filepath.Join(dir, "loaded."+labels["scheduler"]))
	writeFile(t, filepath.Join(dir, "fail."+labels["scheduler"]), "9")
	if _, err := startLoginItems(labels); err == nil || !strings.Contains(err.Error(), "Input/output error") {
		t.Fatal(err)
	}
}

// stop boots the scheduler out, and the menu bar app's agent unless it's
// the process that asked (it quits on its own); then any other menu bar app
// running from the app's folder.
func TestStopBootsOutTheSchedulerAndTheMenuBarApp(t *testing.T) {
	dir, calls := fakeLaunchctl(t)
	labels := loginItemLabels()
	writeFile(t, filepath.Join(dir, "loaded."+labels["scheduler"]), "")
	writeFile(t, filepath.Join(dir, "loaded."+labels["ui"]), "4242")
	out, err := stopLoginItems(labels, t.TempDir(), []int{os.Getpid(), 4242})
	if err != nil || !out.Changed || out.Scheduler != "stopped" || out.UIStopped {
		t.Fatal(out, err)
	}
	if !hasCall(calls(), "launchctl bootout "+launchdDomain()+"/"+labels["scheduler"]) ||
		hasCall(calls(), "launchctl bootout "+launchdDomain()+"/"+labels["ui"]) {
		t.Fatalf("%q", calls())
	}
	// Stopped already: nothing to do. A menu bar app launchd runs that
	// didn't ask is booted out.
	out, err = stopLoginItems(labels, t.TempDir(), []int{os.Getpid()})
	if err != nil || out.Changed || !out.UIStopped || !hasCall(calls(), "launchctl bootout "+launchdDomain()+"/"+labels["ui"]) {
		t.Fatal(out, err, calls())
	}
	// One opened from Finder (no agent) is sent SIGTERM.
	sleeper, exited := startSleeper(t)
	uiExecutable = filepath.Base(sleeper.Path)
	t.Cleanup(func() { uiExecutable = "AutomatorUI" })
	out, err = stopLoginItems(labels, filepath.Dir(sleeper.Path), []int{os.Getpid()})
	if err != nil || !out.UIStopped || !exited() {
		t.Fatal(out, err)
	}
}

func TestProcessesRunningReadsPS(t *testing.T) {
	ps := "  101 /Users/me/Applications/Acme Automator.app/Contents/MacOS/AutomatorUI\n" +
		"  102 /Users/me/Applications/Other.app/Contents/MacOS/AutomatorUI\n  103 /bin/zsh\n"
	got := processesRunning(ps, "/Users/me/Applications/Acme Automator.app/Contents/MacOS/AutomatorUI")
	if len(got) != 1 || got[0] != 101 {
		t.Fatal(got)
	}
}
