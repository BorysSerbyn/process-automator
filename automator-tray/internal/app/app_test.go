package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tree lays out a packaged Linux tree for an app: bin/<command>,
// bin/automator-tray and share/<folder>/{install.env,tray/}. It returns the
// tray's path.
func tree(t *testing.T, root, folder, command, env string) string {
	t.Helper()
	share := filepath.Join(root, "share", folder)
	for _, dir := range []string{filepath.Join(root, "bin"), filepath.Join(share, IconsFolder)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(share, "install.env"), env, 0o644)
	write(t, filepath.Join(root, "bin", command), "#!/bin/sh\n", 0o755)
	tray := filepath.Join(root, "bin", "automator-tray")
	write(t, tray, "", 0o755)
	return tray
}

func write(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

// Any app built on the engine: its names come from its package, never the
// tray's.
func TestDiscoverNamesTheAppFromItsPackage(t *testing.T) {
	root := t.TempDir()
	exe := tree(t, root, "acme-automator", "acme-auto",
		"OS=linux\nNAME=\"Acme Automator\"\nCOMMAND=acme-auto\nFOLDER=acme-automator\nVERSION=2.1.0\nBUNDLE_ID=com.acme.acme-auto\nUI_LABEL=acme-auto-tray.desktop\n")
	info, err := Discover(exe, "")
	if err != nil {
		t.Fatal(err)
	}
	want := Info{
		Name: "Acme Automator", Command: "acme-auto", CLI: filepath.Join(root, "bin", "acme-auto"), Version: "2.1.0",
		BundleID: "com.acme.acme-auto", Icons: filepath.Join(root, "share", "acme-automator", IconsFolder),
	}
	if info != want {
		t.Fatalf("got %+v\nwant %+v", info, want)
	}
}

func TestDiscoverRefusesWhatIsntAnApp(t *testing.T) {
	for name, c := range map[string]struct{ env, want string }{
		"no names":  {"OS=linux\nVERSION=1\n", "doesn't name the app"},
		"no CLI":    {"NAME=X\nCOMMAND=other\n", "no X CLI"},
		"bad names": {"NAME=X\nCOMMAND=../x\n", "doesn't name the app"},
	} {
		t.Run(name, func(t *testing.T) {
			exe := tree(t, t.TempDir(), "x", "x", c.env)
			if _, err := Discover(exe, ""); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want %q", err, c.want)
			}
		})
	}
	t.Run("not packaged", func(t *testing.T) {
		if _, err := Discover(filepath.Join(t.TempDir(), "bin", "automator-tray"), ""); err == nil ||
			!strings.Contains(err.Error(), "no app beside") {
			t.Fatalf("got %v", err)
		}
	})
}

// A share folder given outright (development) is used as is.
func TestDiscoverInAGivenFolder(t *testing.T) {
	root := t.TempDir()
	exe := tree(t, root, "f", "cmd", "NAME=N\nCOMMAND=cmd\n")
	info, err := Discover(exe, filepath.Join(root, "share", "f"))
	if err != nil || info.Name != "N" || info.Version != "dev" {
		t.Fatalf("%+v %v", info, err)
	}
}
