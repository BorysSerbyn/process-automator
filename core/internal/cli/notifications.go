package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/history"
	"github.com/BorysSerbyn/process-automator/core/internal/notify"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
)

// The core records notifications; frontends deliver them (see package notify).

// ClickOutput is `notifications open --json`: what clicking did.
type ClickOutput struct {
	Notification string `json:"notification"`
	// Action is the click action carried out: show-run, open, run or none.
	Action string `json:"action"`
	// Target is what was opened, or the command run.
	Target string `json:"target,omitempty"`
	// Routine and Run, for show-run: with --frontend-shows-runs the frontend
	// shows them itself; otherwise a report of the run was opened (Target).
	Routine string `json:"routine,omitempty"`
	Run     string `json:"run,omitempty"`
}

func newNotificationsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "notifications", Short: "Notifications the core recorded for frontends to deliver"}
	cmd.AddCommand(newNotificationsListCmd(), newNotificationsDeliveredCmd(), newNotificationsOpenCmd())
	return cmd
}

func newNotificationsListCmd() *cobra.Command {
	var pending, asJSON bool
	cmd := &cobra.Command{
		Use:         "list",
		Short:       "List notifications, newest last",
		Annotations: readOnly(),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := loadApp(cmd)
			if err != nil {
				return err
			}
			f, err := notify.Load(a.Layout.Notifications())
			if err != nil {
				return err
			}
			list := f.Notifications
			if pending {
				list = list[:0:0]
				for _, n := range f.Notifications {
					if n.DeliveredAt == nil {
						list = append(list, n)
					}
				}
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), list)
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tAT\tKIND\tTITLE\tDELIVERED")
			for _, n := range list {
				delivered := "no"
				if n.DeliveredAt != nil {
					delivered = "yes"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", n.ID, n.At.Local().Format("Mon Jan 2 15:04"), n.Kind, n.Title, delivered)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&pending, "pending", false, "only those not delivered yet")
	jsonFlag(cmd.Flags(), &asJSON, []notify.Notification{})
	return cmd
}

func newNotificationsDeliveredCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "delivered <id>...",
		Short:       "Mark notifications delivered (frontends call this after showing them)",
		Annotations: map[string]string{annNotForRoutines: "only frontends deliver notifications"},
		Args:        cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := loadApp(cmd)
			if err != nil {
				return err
			}
			return notify.MarkDelivered(a.Layout.Notifications(), args, time.Now())
		},
	}
}

func newNotificationsOpenCmd() *cobra.Command {
	var frontendShowsRuns, asJSON bool
	cmd := &cobra.Command{
		Use:   "open <id>",
		Short: "Do what clicking a notification does (frontends call this on a click)",
		Long: "Carries out the notification's click action: the file it asked to open, else the routine's " +
			"onNotificationClick, else notifications.on-click (default show-run). show-run opens a readable " +
			"report of the run, or with --frontend-shows-runs leaves showing it to the frontend.",
		Annotations: map[string]string{annNotForRoutines: "clicking is mine"},
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := loadApp(cmd)
			if err != nil {
				return err
			}
			n, err := notify.Get(a.Layout.Notifications(), args[0])
			if err != nil {
				return err
			}
			act, err := clickAction(a, n)
			if err != nil {
				return err
			}
			out := ClickOutput{Notification: n.ID, Action: act.Kind, Target: act.Target}
			switch act.Kind {
			case notify.ActionShowRun:
				out.Routine, out.Run = n.Routine, n.Run
				if !frontendShowsRuns && n.Run != "" {
					if out.Target, err = openRunReport(a, n.Run); err != nil {
						return err
					}
				}
			case notify.ActionOpen:
				target, err := settings.ExpandPath(act.Target)
				if err != nil {
					return err
				}
				out.Target = target
				if err := openExternal(target); err != nil {
					return err
				}
			case notify.ActionRun:
				if err := startShell(act.Target, clickEnv(n)); err != nil {
					return err
				}
			}
			return emit(cmd, out, fmt.Sprintf("%s %s", out.Action, out.Target))
		},
	}
	cmd.Flags().BoolVar(&frontendShowsRuns, "frontend-shows-runs", false, "the frontend shows the run itself (show-run opens nothing)")
	jsonFlag(cmd.Flags(), &asJSON, ClickOutput{})
	return cmd
}

// clickAction is what clicking n does: the file it asked to open, else its
// routine's onNotificationClick, else notifications.on-click.
func clickAction(a *engine.App, n notify.Notification) (notify.Action, error) {
	if n.Open != "" {
		return notify.Action{Kind: notify.ActionOpen, Target: n.Open}, nil
	}
	if n.Routine != "" {
		if set, err := routines.Load(a.Layout.Routines()); err == nil {
			if r, err := set.Find(n.Routine); err == nil {
				if c, err := a.RoutineConfig(r); err == nil && c.OnClick != "" {
					return notify.ParseAction(c.OnClick)
				}
			}
		}
	}
	vals, err := a.Settings()
	if err != nil {
		return notify.Action{}, err
	}
	return notify.ParseAction(vals.String("notifications.on-click"))
}

// clickEnv tells a click command what was clicked.
func clickEnv(n notify.Notification) []string {
	return []string{
		"PROCAUTO_NOTIFICATION_ID=" + n.ID, "PROCAUTO_NOTIFICATION_TITLE=" + n.Title,
		"PROCAUTO_NOTIFICATION_BODY=" + n.Body, "PROCAUTO_ROUTINE=" + n.Routine, "PROCAUTO_RUN=" + n.Run,
	}
}

// reportsKept is how long a run report written for opening is kept.
const reportsKept = 7 * 24 * time.Hour

// pruneReports removes reports written more than reportsKept ago: they're
// only for opening, and the run history holds the runs.
func pruneReports(dir string, now time.Time) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > reportsKept {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// openRunReport writes a readable report of a run (as `runs show` prints it)
// and opens it: show-run until a frontend can show runs itself.
func openRunReport(a *engine.App, runID string) (string, error) {
	e, err := history.Get(a.Layout.History(), runID)
	if err != nil {
		return "", err
	}
	dir := a.Layout.Reports()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	pruneReports(dir, time.Now())
	path := filepath.Join(dir, strings.ReplaceAll(runID, "/", "_")+".txt")
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	printRun(f, e)
	if err := f.Close(); err != nil {
		return "", err
	}
	return path, openExternal(path)
}
