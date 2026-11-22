package loginitems

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sleeperEnv makes the test binary a process that just waits (TestMain):
// a frontend for login-items stop to stop.
const sleeperEnv = "PROCAUTO_TEST_SLEEPER"

// startSleeper runs this test binary as a stand-in frontend, and reports
// whether it has exited.
func startSleeper(t *testing.T) (*exec.Cmd, func() bool) {
	t.Helper()
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^$")
	cmd.Env = append(os.Environ(), sleeperEnv+"=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
	exited := func() bool {
		select {
		case <-done:
			return true
		case <-time.After(5 * time.Second):
			return false
		}
	}
	return cmd, exited
}

// fakeTool puts a shell script named name first on PATH: it logs each call
// ("name args") to the returned log and runs body.
func fakeTool(t *testing.T, name, body string) (dir string, calls func() []string) {
	t.Helper()
	dir = t.TempDir()
	script := "#!/bin/sh\nd=$(dirname \"$0\")\necho \"" + name + " $*\" >> \"$d/log\"\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir, func() []string {
		b, _ := os.ReadFile(filepath.Join(dir, "log"))
		return strings.Split(strings.TrimSpace(string(b)), "\n")
	}
}

func hasCall(calls []string, prefix string) bool {
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}
