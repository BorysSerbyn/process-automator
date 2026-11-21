package cli

import (
	"os"
	"path/filepath"
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
