package cli

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
)

// StartOutput is `start --json`: a run started in the background.
type StartOutput struct {
	Routine string `json:"routine"`
	DryRun  bool   `json:"dryRun"`
	// Log is where the run's output goes; its outcome is in run status and
	// the run history, as for any run.
	Log string `json:"log"`
}

func newStartCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "start <routine>",
		Short: "Start a routine's run (either kind) in the background and return at once (frontends: run now)",
		Long: "Runs the routine as `run` does, detached, so it keeps going whatever started it. Follow it in run " +
			"status (running, queued) and the run history; `stop` stops it.",
		Annotations: map[string]string{annNotReadOnly: "it runs a routine"},
		Args:        cobra.ExactArgs(1),
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, args []string) error {
			x, err := a.Find(args[0])
			if err != nil {
				return err
			}
			out := StartOutput{Routine: x.Name(), DryRun: dryRun, Log: a.Layout.StartLog()}
			if err := startDetachedRun(a, x.Name(), dryRun, out.Log); err != nil {
				return err
			}
			if wantsJSON(cmd) {
				return writeJSON(cmd.OutOrStdout(), out)
			}
			kind := "Started"
			if dryRun {
				kind = "Started a dry run of"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s in the background (log: %s).\n", kind, x.Name(), out.Log)
			return nil
		}),
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "intercept every side-effecting tool call (nothing is posted or written); a program that writes gets --dry-run")
	jsonFlag(cmd.Flags(), StartOutput{})
	return cmd
}

// startDetachedRun runs `run <routine>` detached, appending its output to log.
func startDetachedRun(a *engine.App, name string, dryRun bool, log string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logf, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = logf.Close() }()
	args := []string{"--config", a.Layout.Config(), "run", name}
	if dryRun {
		args = append(args, "--dry-run")
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = logf, logf
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
