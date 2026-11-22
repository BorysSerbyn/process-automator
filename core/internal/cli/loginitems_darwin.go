package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/product"
)

// On macOS, login items are LaunchAgents installed by scripts/install-app.sh;
// starting at login is launchd's enable/disable override for them.

// loginItemLabels are the app's LaunchAgents: automator-ui and the scheduler,
// named after the bundle ID (as the Makefile's UI_LABEL and SCHED_LABEL).
func loginItemLabels() map[string]string {
	return map[string]string{"ui": product.BundleID() + ".ui", "scheduler": product.BundleID() + ".scheduler"}
}

func launchdDomain() string { return fmt.Sprintf("gui/%d", os.Getuid()) }

// scripts/install-app.sh reads the same output with grep; keep the two in step.
var disabledLine = regexp.MustCompile(`"([^"]+)"\s*=>\s*(disabled|true|enabled|false)`)

func loginItems(labels map[string]string) ([]LoginItem, error) {
	out, err := exec.Command("launchctl", "print-disabled", launchdDomain()).Output()
	if err != nil {
		return nil, fmt.Errorf("launchctl print-disabled: %w", err)
	}
	disabled := map[string]bool{}
	for _, m := range disabledLine.FindAllStringSubmatch(string(out), -1) {
		disabled[m[1]] = m[2] == "disabled" || m[2] == "true"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	var items []LoginItem
	for name, label := range labels {
		_, statErr := os.Stat(filepath.Join(home, "Library", "LaunchAgents", label+".plist"))
		items = append(items, LoginItem{Name: name, Label: label, Installed: statErr == nil, Enabled: !disabled[label]})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}

func setLoginItems(labels map[string]string, on bool) error {
	verb := "disable"
	if on {
		verb = "enable"
	}
	var errs []error
	for _, label := range labels {
		if out, err := exec.Command("launchctl", verb, launchdDomain()+"/"+label).CombinedOutput(); err != nil {
			errs = append(errs, fmt.Errorf("launchctl %s %s: %v: %s", verb, label, err, out))
		}
	}
	return errors.Join(errs...)
}

// uiName is what the frontend is called here.
const uiName = "menu bar app"

// uiExecutable is the menu bar app's binary, beside the core in the .app.
var uiExecutable = "AutomatorUI"

// The waits around launchd (tests shorten them): an agent stopping (the
// scheduler waits up to 30s for its runs), and launchd letting go of one
// just stopped before it loads it again.
var (
	agentExitWait  = time.Minute
	bootstrapRetry = 2 * time.Second
)

var pidLine = regexp.MustCompile(`(?m)^\s*pid = (\d+)\s*$`)

// launchAgent is whether launchd has the agent loaded in this session, and its
// process, if it runs (0: none).
func launchAgent(label string) (loaded bool, pid int) {
	out, err := exec.Command("launchctl", "print", launchdDomain()+"/"+label).Output()
	if err != nil {
		return false, 0 // not loaded (launchctl print fails for those)
	}
	if m := pidLine.FindSubmatch(out); m != nil {
		pid, _ = strconv.Atoi(string(m[1]))
	}
	return true, pid
}

// isSchedulerRunning: the scheduler's agent is loaded (KeepAlive keeps it
// running while it is).
func isSchedulerRunning(label string) (bool, error) {
	loaded, _ := launchAgent(label)
	return loaded, nil
}

// bootout unloads the agent for this session (until it's loaded again, or
// the next login: KeepAlive doesn't bring it back) and waits for its process
// to exit, as install-app.sh's stop_agent does: loading it again before then
// fails.
func bootout(label string) error {
	loaded, pid := launchAgent(label)
	if !loaded {
		return nil
	}
	if out, err := exec.Command("launchctl", "bootout", launchdDomain()+"/"+label).CombinedOutput(); err != nil {
		if still, _ := launchAgent(label); still {
			return fmt.Errorf("launchctl bootout %s: %v: %s", label, err, strings.TrimSpace(string(out)))
		}
	}
	if pid != 0 {
		waitExit(pid, agentExitWait)
	}
	return nil
}

func stopScheduler(label string) error { return bootout(label) }

// startScheduler loads the scheduler's agent, which runs it. launchd
// refuses to load an agent turned off at login, so it's turned on for the
// load and off again (install-app.sh's load_agent does the same). A load
// that fails while launchd is still letting go of the agent stopped just
// before is tried again for a few seconds; one that fails because the agent
// got loaded meanwhile (at login) is fine.
func startScheduler(label string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	plist := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
	if _, err := os.Stat(plist); err != nil {
		return fmt.Errorf("the scheduler isn't installed (no %s): install %s first", plist, product.Name)
	}
	job := launchdDomain() + "/" + label
	if out, err := exec.Command("launchctl", "print-disabled", launchdDomain()).Output(); err == nil {
		for _, m := range disabledLine.FindAllStringSubmatch(string(out), -1) {
			if m[1] == label && (m[2] == "disabled" || m[2] == "true") {
				if out, err := exec.Command("launchctl", "enable", job).CombinedOutput(); err != nil {
					return fmt.Errorf("launchctl enable %s: %v: %s", label, err, out)
				}
				defer func() { _ = exec.Command("launchctl", "disable", job).Run() }()
			}
		}
	}
	var last error
	for try := 0; try < 5; try++ {
		if try > 0 {
			time.Sleep(bootstrapRetry)
		}
		out, err := exec.Command("launchctl", "bootstrap", launchdDomain(), plist).CombinedOutput()
		if err == nil {
			return nil
		}
		if loaded, _ := launchAgent(label); loaded {
			return nil
		}
		last = fmt.Errorf("launchctl bootstrap %s: %v: %s", label, err, strings.TrimSpace(string(out)))
	}
	return last
}

// stopFrontends stops the menu bar app: its agent's (booted out, as
// SIGTERM alone would have launchd start it again) and any other one running
// from dir (opened from Finder), except the processes spared. A spared one
// launchd started is left loaded: it exits on its own, successfully, which
// its agent doesn't restart.
func stopFrontends(uiLabel, dir string, spare []int) (bool, error) {
	stopped := false
	var errs []error
	if loaded, pid := launchAgent(uiLabel); loaded && !spared(pid, spare) {
		if err := bootout(uiLabel); err != nil {
			errs = append(errs, err)
		} else if pid != 0 {
			stopped = true
		}
	}
	out, err := exec.Command("ps", "-x", "-o", "pid=,comm=").Output()
	if err != nil {
		return stopped, errors.Join(append(errs, fmt.Errorf("ps: %w", err))...)
	}
	for _, pid := range processesRunning(string(out), filepath.Join(dir, uiExecutable)) {
		if spared(pid, spare) {
			continue
		}
		if err := syscall.Kill(pid, syscall.SIGTERM); err == nil {
			stopped = true
		}
	}
	return stopped, errors.Join(errs...)
}

// processesRunning are the processes in ps's "pid comm" lines whose
// executable is exe (macOS's comm is the executable's full path, as it was
// started).
func processesRunning(ps, exe string) []int {
	var pids []int
	for _, line := range strings.Split(ps, "\n") {
		pidText, comm, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || strings.TrimSpace(comm) != exe {
			continue
		}
		if pid, err := strconv.Atoi(pidText); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

// waitExit waits (up to limit) for process pid to exit.
func waitExit(pid int, limit time.Duration) {
	deadline := time.Now().Add(limit)
	for syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
}
