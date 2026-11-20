package cli

import (
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
	"github.com/BorysSerbyn/process-automator/core/internal/schedule"
	"github.com/BorysSerbyn/process-automator/core/internal/scheduler"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
)

// RoutineInfo is one row of `routines list --json`: a routine with its
// effective settings (file values overridden by settings).
type RoutineInfo struct {
	Name    string   `json:"name"`
	Path    string   `json:"path"`
	Servers []string `json:"servers"`
	Tools   []string `json:"tools"`
	// Model is the routine's model as written in its file (empty: the backend's default).
	Model   string `json:"model,omitempty"`
	Enabled bool   `json:"enabled"`
	// Schedule is the cron schedule; absent for routines run only by hand.
	Schedule string `json:"schedule,omitempty"`
	// ScheduleText is the schedule in plain words ("At 09:20, Monday through Friday").
	ScheduleText string `json:"scheduleText,omitempty"`
	Staleness    string `json:"staleness,omitempty"`
	Timeout      string `json:"timeout"`
	// Effort is the routine's effort setting ("default" leaves it to the backend).
	Effort string `json:"effort"`
	// Folder is where its runs work (absent: a temporary folder); frontends
	// can filter routines by it.
	Folder string `json:"folder,omitempty"`
	// Session is "save" when runs keep their agent CLI session, else "discard".
	Session string `json:"session"`
	// BuiltinTools are the agent CLI built-in tools its runs may use.
	BuiltinTools []string `json:"builtinTools"`
	// Trusted is whether it may run for real (what it can reach is what I
	// trusted); Untrusted says why not.
	Trusted   bool   `json:"trusted"`
	Untrusted string `json:"untrusted,omitempty"`
	// AgentModel and AgentEffort are the model and effort as the configured
	// backend's agent CLI takes them (e.g. claude --model/--effort), worked out
	// by the core so frontends never translate; empty means the CLI's default.
	AgentModel  string `json:"agentModel,omitempty"`
	AgentEffort string `json:"agentEffort,omitempty"`
	// Next is the next scheduled run (absent when disabled or manual-only).
	Next *time.Time `json:"next,omitempty"`
	// LastRun is the latest outcome from run status.
	LastRun *runstatus.Run `json:"lastRun,omitempty"`
}

func newRoutinesCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "routines", Short: "Routines installed on this device (one .prompt file each)"}
	var asJSON bool
	list := &cobra.Command{
		Use:         "list",
		Annotations: readOnly(),
		Short:       "List routine files with their schedule and enabled state",
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := loadApp(cmd)
			if err != nil {
				return err
			}
			dir := a.Layout.Routines()
			set, err := routines.Load(dir)
			if err != nil {
				return err
			}
			warnBroken(cmd, set)
			status, err := runstatus.Load(a.Layout.RunStatus())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: can't read run status: %v\n", err)
			}
			now := time.Now()
			def, err := a.BackendDef()
			if err != nil {
				return err
			}
			rows := make([]RoutineInfo, 0, len(set.Routines))
			for _, r := range set.Routines {
				rows = append(rows, routineInfo(cmd, a, def, status, r, now))
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), rows)
			}
			out := cmd.OutOrStdout()
			if len(rows) == 0 {
				fmt.Fprintf(out, "No routines. Add <name>.prompt files to %s (shared snippets: _<name>.prompt)\n", dir)
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tENABLED\tSCHEDULE\tNEXT\tLAST RUN")
			for _, r := range rows {
				sched, next, last := r.Schedule, "-", "-"
				if sched == "" {
					sched = "(manual)"
				}
				if r.Next != nil {
					next = r.Next.Format("Mon Jan 2 15:04")
				}
				if l := r.LastRun; l != nil {
					last = fmt.Sprintf("%s %s", l.Outcome, l.StartedAt.Local().Format("Mon Jan 2 15:04"))
				}
				fmt.Fprintf(tw, "%s\t%v\t%s\t%s\t%s\n", r.Name, r.Enabled, sched, next, last)
			}
			_ = tw.Flush()
			fmt.Fprintf(out, "\nFolder: %s\n", dir)
			return nil
		},
	}
	jsonFlag(list.Flags(), &asJSON, []RoutineInfo{})
	cmd.AddCommand(list, newRoutinesShowCmd(), newRoutinesCreateCmd(), newRoutinesUpdateCmd(), newRoutinesDeleteCmd(), newRoutinesCheckCmd(), newRoutinesTrustCmd(), newRoutinesPauseCmd(false), newRoutinesPauseCmd(true))
	return cmd
}

// routineInfo is a routine with its effective settings and last run; problems
// with its settings are warned about, not fatal.
func routineInfo(cmd *cobra.Command, a *engine.App, def engine.BackendDef, status *runstatus.Status, r routines.Routine, now time.Time) RoutineInfo {
	rd := a.Assess(r)
	c := rd.Config
	if rd.ConfigErr != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s: %v\n", r.Name, rd.ConfigErr)
	}
	row := RoutineInfo{
		Name: r.Name, Path: r.Path, Servers: r.Servers, Tools: r.Tools, Model: r.Model,
		Enabled: c.Enabled, Schedule: c.Schedule, Timeout: settings.DurationText(c.Timeout), Effort: c.Effort,
		Folder: c.Folder, Session: "discard", BuiltinTools: c.BuiltinTools,
	}
	if c.SaveSession {
		row.Session = "save"
	}
	if row.BuiltinTools == nil {
		row.BuiltinTools = []string{}
	}
	if rd.ConfigErr == nil {
		if rd.TrustErr != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s: %v\n", r.Name, rd.TrustErr)
		} else {
			row.Trusted, row.Untrusted = rd.Untrusted == "", rd.Untrusted
		}
	}
	if c.Schedule != "" {
		row.Staleness = settings.DurationText(c.Staleness)
		row.ScheduleText = schedule.Text(c.Schedule)
	}
	var err error
	if row.AgentModel, err = def.AgentModel(r.Model); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s: %v\n", r.Name, err)
	}
	if c.Effort != "default" {
		row.AgentEffort = c.Effort
	}
	if c.Enabled && c.Schedule != "" {
		if n := scheduler.Next(c.Schedule, now); !n.IsZero() {
			row.Next = &n
		}
	}
	if status != nil {
		for _, s := range status.Routines {
			if s.Name == r.Name {
				row.LastRun = s.LastRun
			}
		}
	}
	return row
}

// warnBroken reports routine files that couldn't be loaded (they're skipped).
func warnBroken(cmd *cobra.Command, set *routines.Set) {
	for _, e := range set.Errors {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: skipping %v\n", e)
	}
}
