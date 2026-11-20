package engine

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
	"github.com/BorysSerbyn/process-automator/core/internal/secrets"
)

// The package's own tests can't use enginetest (it imports the engine).

// testApp is an app on an empty data folder.
func testApp(t *testing.T) *App {
	t.Helper()
	a, err := Load(filepath.Join(t.TempDir(), "config.yaml"), nil, secrets.NewMemory())
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// waitOutcome waits until a routine's last run in run status has the outcome.
func waitOutcome(t *testing.T, a *App, name string, want runstatus.Outcome) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		s, _ := runstatus.Load(a.Layout.RunStatus())
		for _, r := range s.Routines {
			if r.Name == name && r.LastRun != nil && r.LastRun.Outcome == want {
				return
			}
		}
	}
	t.Fatalf("%s never became %s", name, want)
}
