package cli

import (
	"errors"
	"fmt"
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
	cmd := &cobra.Command{
		Use:   "stop <routine>",
		Short: "Stop a routine's run in progress (or waiting for its turn); it's recorded as stopped",
		Long: "Asks the run to stop, whichever process runs it (the scheduler, a terminal, a frontend), and waits " +
			"for it to end. A stopped run is recorded as stopped, not as a failure.",
		Annotations: map[string]string{annNotReadOnly: "it stops runs"},
		Args:        cobra.ExactArgs(1),
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, args []string) error {
			name := args[0]
			if !inProgress(a, name) {
				return fmt.Errorf("%s isn't running", name)
			}
			if err := a.RequestStop(name); err != nil {
				return err
			}
			deadline := time.Now().Add(stopWait)
			for inProgress(a, name) {
				if time.Now().After(deadline) {
					a.CancelStop(name)
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
		}),
	}
	jsonFlag(cmd.Flags(), StopOutput{})
	return cmd
}

func lastRun(a *engine.App, name string) *runstatus.Run {
	s, _ := runstatus.Load(a.Layout.RunStatus())
	return s.LastRun(name)
}

func inProgress(a *engine.App, name string) bool {
	run := lastRun(a, name)
	return run != nil && (run.Outcome == runstatus.OutcomeRunning || run.Outcome == runstatus.OutcomeQueued)
}
