package loginitems

import "testing"

const entry = "[Desktop Entry]\nType=Application\nName=Acme Automator\nExec=/opt/acme/bin/automator-tray\n"

func TestAutostart(t *testing.T) {
	if !autostartEnabled(entry) {
		t.Fatal("a plain entry doesn't start at login")
	}
	off := setAutostart(entry, false)
	if off != entry+"Hidden=true\n" || autostartEnabled(off) {
		t.Fatalf("off: %q", off)
	}
	if on := setAutostart(off, true); on != entry || !autostartEnabled(on) {
		t.Fatalf("on: %q", on)
	}
	// GNOME's own switch counts, and turning it on clears it.
	gnome := entry + "X-GNOME-Autostart-enabled=false\n"
	if autostartEnabled(gnome) || setAutostart(gnome, true) != entry {
		t.Fatalf("%q", setAutostart(gnome, true))
	}
	if setAutostart(setAutostart(entry, false), false) != entry+"Hidden=true\n" {
		t.Fatal("turning it off twice")
	}
}
