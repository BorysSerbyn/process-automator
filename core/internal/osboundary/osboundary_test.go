package osboundary

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// rules are what shared (not per-OS) files may not contain.
var rules = []struct {
	name string
	re   *regexp.Regexp
}{
	{"runtime.GOOS (put OS branches in per-OS files)", regexp.MustCompile(`runtime\.GOOS`)},
	{`the syscall package (OS calls go in per-OS files)`, regexp.MustCompile(`"syscall"`)},
	{"running an OS tool", regexp.MustCompile(`exec\.Command(Context)?\((ctx, )?"(open|xdg-open|osascript|launchctl|security|defaults|systemctl|notify-send|powershell|cmd)"`)},
	{"an OS-specific path", regexp.MustCompile(`"(/Applications/|~/Library/|/Library/|/usr/share/|~/.config/|C:\\)`)},
}

// allowed are the exceptions, each with why.
var allowed = map[string]string{
	"internal/routines/routines.go: runtime.GOOS (put OS branches in per-OS files)": "Platform() names the OS for automator.platforms; it's the one place that does",
	"internal/cli/root.go: the syscall package (OS calls go in per-OS files)":       "syscall.SIGTERM is portable (defined on every GOOS)",
	"internal/cli/daemon.go: the syscall package (OS calls go in per-OS files)":     "syscall.SIGTERM is portable (defined on every GOOS)",
}

var perOS = regexp.MustCompile(`_(darwin|linux|windows|freebsd|unix|other|bsd)\.go$`)

func TestSharedFilesStayOSNeutral(t *testing.T) {
	root := filepath.Join("..", "..") // core/
	var found []string
	for _, dir := range []string{"internal", "cmd", "automator", "pluginapi"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || perOS.MatchString(path) {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			src := string(b)
			if strings.Contains(src, "\n//go:build ") || strings.HasPrefix(src, "//go:build ") {
				return nil // a per-OS file by build tag
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			for _, r := range rules {
				if r.re.MatchString(src) {
					key := rel + ": " + r.name
					if _, ok := allowed[key]; !ok {
						found = append(found, key)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range found {
		t.Errorf("%s", f)
	}
}

// Every exception still matches something, so the list doesn't rot.
func TestAllowedStillNeeded(t *testing.T) {
	for key := range allowed {
		file, rule, _ := strings.Cut(key, ": ")
		b, err := os.ReadFile(filepath.Join("..", "..", file))
		if err != nil {
			t.Errorf("%s: %v", key, err)
			continue
		}
		for _, r := range rules {
			if r.name == rule && !r.re.Match(b) {
				t.Errorf("%s no longer needed: remove it from allowed", key)
			}
		}
	}
}
