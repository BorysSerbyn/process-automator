package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/trigger"
)

func newRunCmd() *cobra.Command {
	var dryRun, asJSON bool
	var date string
	cmd := &cobra.Command{
		Use: "run <routine>",
		Annotations: map[string]string{
			annReadOnlyWith:   "dry-run",
			annNotForRoutines: "a routine can't run a routine: runs take turns, so it would wait on itself",
		},
		Short: "Run a routine from the routines folder now (ignores its schedule and enabled flag)",
		Long: "Runs a routine now. With --date it runs as of that day (a backfill): its today, since and now " +
			"inputs are computed for the date, at the routine's first scheduled time that day (or the current " +
			"time of day if it has none). A routine covering the previous workday (digest) is backfilled " +
			"with the day after the one to cover: Friday's digest is --date of the following Monday.",
		Example: "  procauto run standup --dry-run   # everything except posting\n" +
			"  procauto run standup\n" +
			"  procauto run digest --date 2026-11-16   # as of Mon Nov 16: fills Fri Nov 13",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := loadApp(cmd)
			if err != nil {
				return err
			}
			set, err := routines.Load(a.Layout.Routines())
			if err != nil {
				return err
			}
			r, err := set.Find(args[0])
			if err != nil {
				return err
			}
			occ := trigger.New(trigger.Manual, time.Now())
			if date != "" {
				cfg, err := a.RoutineConfig(r)
				if err != nil {
					return err
				}
				if occ, err = engine.Backfill(date, cfg.Schedule, time.Now()); err != nil {
					return err
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "backfill: running %s as of %s\n", r.Name, occ.At.Format("Mon Jan 2 2006 15:04"))
			}
			res, err := a.Execute(cmd.Context(), set, r, engine.RunOptions{
				DryRun: dryRun, Occurrence: occ, Log: cmd.ErrOrStderr(),
			})
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			printResult(cmd, res)
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "intercept every side-effecting tool call (nothing is posted or written)")
	jsonFlag(cmd.Flags(), &asJSON, agent.Result{})
	cmd.Flags().StringVar(&date, "date", "", "run as of this day, YYYY-MM-DD (today or earlier): a backfill")
	return cmd
}

func printResult(cmd *cobra.Command, res *agent.Result) {
	out := cmd.OutOrStdout()
	if res.DryRun {
		for _, w := range res.Writes {
			fmt.Fprintf(out, "\n--- DRY RUN: would call %s ---\n", w.Tool)
			for k, v := range w.Args {
				if s, ok := v.(string); ok && strings.Contains(s, "\n") {
					fmt.Fprintf(out, "%s:\n%s\n", k, s)
				} else {
					fmt.Fprintf(out, "%s: %v\n", k, v)
				}
			}
		}
	}
	fmt.Fprintf(out, "\n%s\n", strings.TrimSpace(res.Text))
	fmt.Fprintln(cmd.ErrOrStderr(), "\n["+res.Summary()+"]")
}
