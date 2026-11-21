package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

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

// itemFile is where a login item is: autostart entries in autostart/, units
// in systemd/user/.
func itemFile(config, label string) string {
	if strings.HasSuffix(label, ".desktop") {
		return filepath.Join(config, "autostart", label)
	}
	return filepath.Join(config, "systemd", "user", label)
}

func loginItems(labels map[string]string) ([]LoginItem, error) {
	config, err := configDir()
	if err != nil {
		return nil, err
	}
	var items []LoginItem
	for name, label := range labels {
		b, readErr := os.ReadFile(itemFile(config, label))
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
	config, err := configDir()
	if err != nil {
		return err
	}
	verb := "disable"
	if on {
		verb = "enable"
	}
	var errs []error
	for _, label := range labels {
		if strings.HasSuffix(label, ".desktop") {
			path := itemFile(config, label)
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
