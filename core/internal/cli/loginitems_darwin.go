package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
)

// On macOS, login items are LaunchAgents installed by scripts/install-app.sh;
// starting at login is launchd's enable/disable override for them.

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
