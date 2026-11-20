package statusline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallKeepsMineAndRestoresIt(t *testing.T) {
	dir := t.TempDir()
	settings, savedPath := filepath.Join(dir, "settings.json"), filepath.Join(dir, "statusline.json")
	mine := `{
  "model": "opus",
  "statusLine": {
    "type": "command",
    "command": "bash ~/.claude/statusline-codeburn.sh",
    "refreshInterval": 60
  },
  "hooks": {}
}
`
	_ = os.WriteFile(settings, []byte(mine), 0o600)
	ours := "'/x/procauto' usage statusline"
	if err := Install(settings, savedPath, ours); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(settings)
	got := string(b)
	if !strings.Contains(got, `"command": "'/x/procauto' usage statusline"`) || !strings.Contains(got, `"refreshInterval": 60`) {
		t.Errorf("installed:\n%s", got)
	}
	if strings.Index(got, `"model"`) > strings.Index(got, `"statusLine"`) || strings.Index(got, `"statusLine"`) > strings.Index(got, `"hooks"`) {
		t.Errorf("key order changed:\n%s", got)
	}
	if ok, _ := Installed(settings); !ok {
		t.Error("not installed")
	}
	if orig, _ := Original(savedPath); orig != "bash ~/.claude/statusline-codeburn.sh" {
		t.Errorf("original %q", orig)
	}
	// Installing again (the app moved) keeps the original.
	_ = Install(settings, savedPath, "'/y/procauto' usage statusline")
	if orig, _ := Original(savedPath); orig != "bash ~/.claude/statusline-codeburn.sh" {
		t.Errorf("original after reinstall %q", orig)
	}
	if err := Uninstall(settings, savedPath); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(settings)
	if !strings.Contains(string(b), `"command": "bash ~/.claude/statusline-codeburn.sh"`) {
		t.Errorf("not restored:\n%s", b)
	}
	if ok, _ := Installed(settings); ok {
		t.Error("still installed")
	}
}

func TestInstallWithoutAStatusLine(t *testing.T) {
	dir := t.TempDir()
	settings, savedPath := filepath.Join(dir, "settings.json"), filepath.Join(dir, "statusline.json")
	if err := Install(settings, savedPath, "procauto usage statusline"); err != nil {
		t.Fatal(err)
	}
	if orig, _ := Original(savedPath); orig != "" {
		t.Errorf("original %q", orig)
	}
	_ = Uninstall(settings, savedPath)
	b, _ := os.ReadFile(settings)
	if strings.Contains(string(b), "statusLine") {
		t.Errorf("a status line was left:\n%s", b)
	}
}
