package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
)

// StopOutput is `stop --json`: how the stopped run ended.
type StopOutput struct {
	Routine string            `json:"routine"`
	Outcome runstatus.Outcome `json:"outcome"`
	Reason  string            `json:"reason,omitempty"`
}

// stopWait is how long `stop` waits for the run to end.
const stopWait = time.Minute

func newStopCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "stop <routine>",
		Short: "Stop a routine's run in progress (or waiting for its turn); it's recorded as stopped",
		Long: "Asks the run to stop, whichever process runs it (the scheduler, a terminal, a frontend), and waits " +
			"for it to end. A stopped run is recorded as stopped, not as a failure.",
		Annotations: map[string]string{annNotForRoutines: "a routine can't stop runs"},
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := loadApp(cmd)
			if err != nil {
				return err
			}
			name := args[0]
			if !inProgress(a, name) {
				return fmt.Errorf("%s isn't running", name)
			}
			req := a.Layout.StopRequest(name)
			if err := os.MkdirAll(filepath.Dir(req), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(req, []byte(time.Now().Format(time.RFC3339)), 0o600); err != nil {
				return err
			}
			deadline := time.Now().Add(stopWait)
			for inProgress(a, name) {
				if time.Now().After(deadline) {
					_ = os.Remove(req)
					return errors.New("asked the run to stop, but it hasn't ended; if its process is gone, the scheduler marks it interrupted when it next starts")
				}
				select {
				case <-cmd.Context().Done():
					return cmd.Context().Err()
				case <-time.After(250 * time.Millisecond):
				}
			}
			out := StopOutput{Routine: name}
			if run := lastRun(a, name); run != nil {
				out.Outcome, out.Reason = run.Outcome, run.Reason
			}
			return emit(cmd, out, fmt.Sprintf("%s: %s", name, out.Outcome))
		},
	}
	jsonFlag(cmd.Flags(), &asJSON, StopOutput{})
	return cmd
}

func lastRun(a *engine.App, name string) *runstatus.Run {
	s, err := runstatus.Load(a.Layout.RunStatus())
	if err != nil {
		return nil
	}
	for _, r := range s.Routines {
		if r.Name == name {
			return r.LastRun
		}
	}
	return nil
}

func inProgress(a *engine.App, name string) bool {
	run := lastRun(a, name)
	return run != nil && (run.Outcome == runstatus.OutcomeRunning || run.Outcome == runstatus.OutcomeQueued)
}
