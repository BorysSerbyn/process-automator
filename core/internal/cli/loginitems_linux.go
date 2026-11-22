package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/BorysSerbyn/process-automator/core/internal/product"
)

// On Linux, login items are installed by install-app.sh: the tray, an XDG
// autostart entry (it starts in graphical sessions only; starting at login
// is whether it's hidden), and the scheduler, a systemd user unit (starting
// at login is whether it's enabled).

// loginItemLabels are the tray's autostart entry and the scheduler's unit,
// as the Makefile's UI_LABEL and LINUX_SCHED_LABEL name them.
func loginItemLabels() map[string]string {
	return map[string]string{"ui": product.Command + "-tray.desktop", "scheduler": product.Command + "-scheduler.service"}
}

func configDir() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config"), nil
}

// unitConfigDir is the config folder systemd's user manager reads this
// user's units from: its own XDG_CONFIG_HOME, which needn't be this
// process's, else ~/.config; this process's when there's no manager to ask
// (install-app.sh's units_dir puts the unit there).
func unitConfigDir() (string, error) {
	out, err := exec.Command("systemctl", "--user", "show-environment").Output()
	if err != nil {
		return configDir()
	}
	for _, line := range strings.Split(string(out), "\n") {
		// A value with special characters comes $'quoted': not one to read.
		if v, ok := strings.CutPrefix(line, "XDG_CONFIG_HOME="); ok && v != "" && !strings.HasPrefix(v, "$'") {
			return v, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config"), nil
}

// itemFile is where a login item is: autostart entries in this session's
// autostart/, units in systemd/user/ where systemd reads them.
func itemFile(label string) (string, error) {
	if strings.HasSuffix(label, ".desktop") {
		config, err := configDir()
		return filepath.Join(config, "autostart", label), err
	}
	config, err := unitConfigDir()
	return filepath.Join(config, "systemd", "user", label), err
}

func loginItems(labels map[string]string) ([]LoginItem, error) {
	var items []LoginItem
	for name, label := range labels {
		path, err := itemFile(label)
		if err != nil {
			return nil, err
		}
		b, readErr := os.ReadFile(path)
		it := LoginItem{Name: name, Label: label, Installed: readErr == nil}
		switch {
		case !it.Installed:
		case strings.HasSuffix(label, ".desktop"):
			it.Enabled = autostartEnabled(string(b))
		default:
			// is-enabled exits non-zero for anything but enabled; its output says which.
			out, _ := exec.Command("systemctl", "--user", "is-enabled", label).Output()
			it.Enabled = strings.TrimSpace(string(out)) == "enabled"
		}
		items = append(items, it)
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
		if strings.HasSuffix(label, ".desktop") {
			path, err := itemFile(label)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			b, err := os.ReadFile(path)
			if errors.Is(err, os.ErrNotExist) {
				continue // not installed (install the app first): nothing starts it
			}
			if err == nil {
				err = os.WriteFile(path, []byte(setAutostart(string(b), on)), 0o644)
			}
			if err != nil {
				errs = append(errs, err)
			}
			continue
		}
		if out, err := exec.Command("systemctl", "--user", verb, label).CombinedOutput(); err != nil {
			errs = append(errs, fmt.Errorf("systemctl --user %s %s: %v: %s", verb, label, err, out))
		}
	}
	return errors.Join(errs...)
}

// uiName is what the frontend is called here.
const uiName = "tray"

// frontendExecutables are the tray and its windows app, beside the core in
// the Linux tree (install-app.sh stops them the same way).
var frontendExecutables = []string{"automator-tray", "automator-windows"}

// userSession is whether systemd's user manager is reachable (a login
// session, or lingering).
func userSession() bool {
	return exec.Command("systemctl", "--user", "show-environment").Run() == nil
}

// noUserSession says what to do without one.
func noUserSession(verb string) error {
	return fmt.Errorf("no systemd user session here, so the scheduler can't be %s by its unit: run `%s daemon` "+
		"yourself (it runs until stopped), or log in to a session that has one", verb, product.Command)
}

func isSchedulerRunning(label string) (bool, error) {
	// is-active exits non-zero for anything but active; its output says which.
	out, _ := exec.Command("systemctl", "--user", "is-active", label).Output()
	switch strings.TrimSpace(string(out)) {
	case "active", "activating", "reloading":
		return true, nil
	}
	return false, nil
}

func startScheduler(label string) error {
	if !userSession() {
		return noUserSession("started")
	}
	path, err := itemFile(label)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("the scheduler isn't installed (no %s): install %s first", path, product.Name)
	}
	if out, err := exec.Command("systemctl", "--user", "start", label).CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl --user start %s: %v: %s", label, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func stopScheduler(label string) error {
	if !userSession() {
		return noUserSession("stopped")
	}
	if out, err := exec.Command("systemctl", "--user", "stop", label).CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl --user stop %s: %v: %s", label, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// stopFrontends stops the tray and the windows it opened, running from dir,
// except the processes spared.
func stopFrontends(_, dir string, spare []int) (bool, error) {
	stopped := false
	for _, name := range frontendExecutables {
		for _, pid := range processesRunning("/proc", filepath.Join(dir, name)) {
			if spared(pid, spare) {
				continue
			}
			if err := syscall.Kill(pid, syscall.SIGTERM); err == nil && name == frontendExecutables[0] {
				stopped = true
			}
		}
	}
	return stopped, nil
}

// processesRunning are this user's processes whose executable is exe, from
// proc (others' /proc/<pid>/exe can't be read); one whose file was replaced
// since (an update) still counts.
func processesRunning(proc, exe string) []int {
	entries, err := os.ReadDir(proc)
	if err != nil {
		return nil
	}
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		target, err := os.Readlink(filepath.Join(proc, e.Name(), "exe"))
		if err == nil && strings.TrimSuffix(target, " (deleted)") == exe {
			pids = append(pids, pid)
		}
	}
	return pids
}
