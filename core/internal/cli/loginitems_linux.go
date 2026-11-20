package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// On Linux, login items are systemd user units installed by install-app.sh;
// starting at login is whether the unit is enabled.

func userUnitDir() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "systemd", "user"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user"), nil
}

func loginItems(labels map[string]string) ([]LoginItem, error) {
	dir, err := userUnitDir()
	if err != nil {
		return nil, err
	}
	var items []LoginItem
	for name, label := range labels {
		_, statErr := os.Stat(filepath.Join(dir, label))
		it := LoginItem{Name: name, Label: label, Installed: statErr == nil}
		if it.Installed {
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
		if out, err := exec.Command("systemctl", "--user", verb, label).CombinedOutput(); err != nil {
			errs = append(errs, fmt.Errorf("systemctl --user %s %s: %v: %s", verb, label, err, out))
		}
	}
	return errors.Join(errs...)
}
