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
	{"running an OS tool", regexp.MustCompile(`exec\.Command(Context)?\(([^,()"]+, )?"(open|xdg-open|osascript|launchctl|security|defaults|systemctl|loginctl|journalctl|notify-send|secret-tool|gio|gdbus|dbus-send|caffeinate|pmset|sw_vers|powershell|cmd|/usr/bin/[^"]*|/bin/[^"]*)"`)},
	{"an OS-specific path", regexp.MustCompile(`"(/Applications/|~/Library/|/Library/|Library"|Application Support|/usr/share/|~/.config/|/proc/|/run/user/|C:\\)`)},
	{"an OS-specific package", regexp.MustCompile(`"(golang\.org/x/sys/(unix|windows)|github\.com/godbus/dbus[^"]*)"`)},
}

// Each rule catches what it's about, written the ways code writes it, so the
// test can't quietly stop enforcing.
func TestRulesCatchOSCode(t *testing.T) {
	for _, src := range []string{
		`runtime.GOOS == "linux"`,
		`import "syscall"`,
		`exec.Command("xdg-open", url)`,
		`exec.CommandContext(ctx, "systemctl", "--user")`,
		`exec.CommandContext(c.ctx, "secret-tool", "lookup")`,
		`exec.Command("/usr/bin/security", "-i")`,
		`filepath.Join(home, "Library", "Logs")`,
		`os.ReadFile("/proc/self/exe")`,
		`import "github.com/godbus/dbus/v5"`,
		`import "golang.org/x/sys/unix"`,
	} {
		caught := false
		for _, r := range rules {
			caught = caught || r.re.MatchString(src)
		}
		if !caught {
			t.Errorf("no rule catches %s", src)
		}
	}
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
	for _, dir := range []string{"internal", "cmd", "automator", "pluginapi", "plugins"} {
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
