package cli

import "strings"

// An XDG autostart entry (a .desktop file) starts its program at login in a
// graphical session unless it's hidden (Hidden=true; GNOME's own switch,
// X-GNOME-Autostart-enabled=false, too). The Linux tray's login item is one.

// autostartEnabled is whether an autostart entry starts at login.
func autostartEnabled(entry string) bool {
	for _, line := range strings.Split(entry, "\n") {
		key, value, _ := strings.Cut(strings.TrimSpace(line), "=")
		switch strings.TrimSpace(key) {
		case "Hidden":
			if strings.TrimSpace(value) == "true" {
				return false
			}
		case "X-GNOME-Autostart-enabled":
			if strings.TrimSpace(value) == "false" {
				return false
			}
		}
	}
	return true
}

// setAutostart is the entry turned on or off at login (Hidden=true when off;
// neither switch when on), the rest as it was.
func setAutostart(entry string, on bool) string {
	var out []string
	for _, line := range strings.Split(strings.TrimRight(entry, "\n"), "\n") {
		key, _, _ := strings.Cut(strings.TrimSpace(line), "=")
		if k := strings.TrimSpace(key); k == "Hidden" || k == "X-GNOME-Autostart-enabled" {
			continue
		}
		out = append(out, line)
	}
	if !on {
		out = append(out, "Hidden=true")
	}
	return strings.Join(out, "\n") + "\n"
}
