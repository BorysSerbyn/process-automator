package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/invopop/jsonschema"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/programs"
	"github.com/BorysSerbyn/process-automator/core/internal/trigger"
)

func newRunCmd() *cobra.Command {
	var dryRun bool
	var date string
	cmd := &cobra.Command{
		Use: "run <routine>",
		Annotations: map[string]string{
			annReadOnlyWith: "dry-run",
			annNotReadOnly:  "running a routine, even a dry run, uses the model and takes a turn in the run queue",
		},
		Short: "Run a routine of either kind now (ignores its schedule and enabled flag)",
		Long: "Runs a routine now. With --date it runs as of that day (a backfill): its today, since and now " +
			"inputs are computed for the date, at the routine's first scheduled time that day (or the current " +
			"time of day if it has none). A routine covering the previous workday is backfilled with the day " +
			"after the one to cover: for a Friday, --date is the following Monday.\n\n" +
			"A program runs its executable; on a dry run, one that writes is passed --dry-run and one that " +
			"doesn't runs as it is. It's told what the run is for in $PA_TRIGGER and $PA_FOR, and runs only " +
			"while trusted, dry runs included.",
		Example: "  " + product.Command + " run standup --dry-run   # everything except posting\n" +
			"  " + product.Command + " run standup\n" +
			"  " + product.Command + " run weekly-report --date 2026-11-16   # as if it were Mon Nov 16",
		Args: cobra.ExactArgs(1),
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, args []string) error {
			x, err := a.Find(args[0])
			if err != nil {
				return err
			}
			occ := trigger.New(trigger.Manual, time.Now())
			if date != "" {
				cfg, err := a.RunConfig(x)
				if err != nil {
					return err
				}
				if occ, err = engine.Backfill(date, cfg.Schedule, time.Now()); err != nil {
					return err
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "backfill: running %s as of %s\n", x.Name(), occ.At.Format("Mon Jan 2 2006 15:04"))
			}
			res, err := a.Run(cmd.Context(), x, engine.RunOptions{
				DryRun: dryRun, Occurrence: occ, Log: cmd.ErrOrStderr(),
			})
			if err != nil {
				return err
			}
			out := RunOutput{Result: res.Agent, Kind: res.Kind.Written(), Program: res.Program}
			if wantsJSON(cmd) {
				return writeJSON(cmd.OutOrStdout(), out)
			}
			printResult(cmd, out)
			return nil
		}),
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "intercept every side-effecting tool call (nothing is posted or written); a program that writes gets --dry-run")
	jsonFlag(cmd.Flags(), RunOutput{})
	cmd.Flags().StringVar(&date, "date", "", "run as of this day, YYYY-MM-DD (today or earlier): a backfill")
	return cmd
}

// RunOutput is `run --json`: how the run went. An LLM routine's is the
// agent's result, its fields at the top level as they've always been; a
// program's has kind program and its program section instead, and none of
// the agent's fields.
type RunOutput struct {
	*agent.Result
	// Kind is program for a program; absent for an LLM routine (llm).
	Kind string `json:"kind,omitempty" jsonschema:"enum=llm,enum=program"`
	// Program is a program's run: its exit code, summary and output.
	Program *programs.Execution `json:"program,omitempty"`
}

func printResult(cmd *cobra.Command, o RunOutput) {
	out := cmd.OutOrStdout()
	if p := o.Program; p != nil {
		if p.Summary != "" {
			fmt.Fprintf(out, "%s\n", p.Summary)
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "[exit %d]\n", p.ExitCode)
		return
	}
	res := o.Result
	if res == nil {
		return
	}
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

// JSONSchemaExtend makes none of the agent's fields required: they're there
// only for an LLM routine's run.
func (RunOutput) JSONSchemaExtend(s *jsonschema.Schema) { s.Required = nil }
