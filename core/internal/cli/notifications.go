package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/notifications"
)

// The core records the app's own notifications; frontends deliver them (see package notifications).

// ClickOutput is `notifications open --json`: what clicking a notification
// shows. The frontend shows it; the core opens nothing.
type ClickOutput struct {
	Notification string `json:"notification"`
	// Action is what to show: show-run (the notification is about a run: its
	// routine's window, on that run) or show-app (about the app itself, e.g.
	// an update: the app).
	Action string `json:"action" jsonschema:"enum=show-run,enum=show-app"`
	// Routine and Run, for show-run.
	Routine string `json:"routine,omitempty"`
	Run     string `json:"run,omitempty"`
}

func newNotificationsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "notifications", Short: "Notifications the core recorded for frontends to deliver"}
	cmd.AddCommand(newNotificationsListCmd(), newNotificationsDeliveredCmd(), newNotificationsOpenCmd())
	return cmd
}

func newNotificationsListCmd() *cobra.Command {
	var pending bool
	cmd := &cobra.Command{
		Use:         "list",
		Short:       "List notifications, newest last",
		Annotations: readOnly(),
		Args:        cobra.NoArgs,
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, _ []string) error {
			f, err := notifications.Load(a.Layout.Notifications())
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
			if wantsJSON(cmd) {
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
		}),
	}
	cmd.Flags().BoolVar(&pending, "pending", false, "only those not delivered yet")
	jsonFlag(cmd.Flags(), []notifications.Notification{})
	return cmd
}

func newNotificationsDeliveredCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "delivered <id>...",
		Short:       "Mark notifications delivered (frontends call this after showing them)",
		Annotations: map[string]string{annNotReadOnly: "only frontends deliver notifications"},
		Args:        cobra.MinimumNArgs(1),
		RunE: withApp(func(_ *cobra.Command, a *engine.App, args []string) error {
			return notifications.MarkDelivered(a.Layout.Notifications(), args, time.Now())
		}),
	}
}

func newNotificationsOpenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "open <id>",
		Short: "Say what clicking a notification shows (frontends call this on a click)",
		Long: "Clicking one of the app's notifications opens the app: one about a run shows the run in its " +
			"routine's window (show-run), one about the app itself (an update) shows the app (show-app). " +
			"The frontend shows it; this opens nothing.",
		Annotations: readOnly(),
		Args:        cobra.ExactArgs(1),
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, args []string) error {
			n, err := notifications.Get(a.Layout.Notifications(), args[0])
			if err != nil {
				return err
			}
			out := ClickOutput{Notification: n.ID, Action: "show-app"}
			if n.Run != "" {
				out.Action, out.Routine, out.Run = "show-run", n.Routine, n.Run
			}
			return emit(cmd, out, strings.TrimSpace(out.Action+" "+out.Run))
		}),
	}
	jsonFlag(cmd.Flags(), ClickOutput{})
	return cmd
}
