package cli

import (
	"errors"
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
	// Action is what to show: open-url (the notification has a link: open
	// URL with the system's default handler), show-run (it's about a run: its
	// routine's window, on that run), show-updates (about an update: the
	// app's updates, its window's Updates) or show-app (about the app
	// itself: the app).
	Action string `json:"action" jsonschema:"enum=open-url,enum=show-run,enum=show-updates,enum=show-app"`
	// URL, for open-url: an http or https link.
	URL string `json:"url,omitempty"`
	// Routine and Run, for show-run.
	Routine string `json:"routine,omitempty"`
	Run     string `json:"run,omitempty"`
	// Hook is true when Routine names a hook: show the hook's window.
	Hook bool `json:"hook,omitempty"`
}

func newNotificationsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "notifications", Short: "Notifications the core recorded for frontends to deliver"}
	cmd.AddCommand(newNotificationsListCmd(), newNotificationsDeliveredCmd(), newNotificationsOpenCmd(), newNotificationsAckCmd())
	return cmd
}

func newNotificationsListCmd() *cobra.Command {
	var pending, waiting, acknowledged bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List notifications, newest last",
		Long: "The app's notifications, newest last. Those waiting for me to acknowledge them (a routine's or hook's, " +
			"asked for through a plugin, such as notify_me with acknowledge) have acknowledge set, and no acknowledgedAt until " +
			"`notifications ack`.",
		Annotations: readOnly(),
		Args:        cobra.NoArgs,
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, _ []string) error {
			f, err := notifications.Load(a.Layout.Notifications())
			if err != nil {
				return err
			}
			if waiting && acknowledged {
				return errors.New("--waiting and --acknowledged exclude each other")
			}
			list := f.Notifications
			switch {
			case waiting:
				list = f.Waiting()
			case acknowledged:
				list = f.Acknowledged()
			}
			if pending {
				var undelivered []notifications.Notification
				for _, n := range list {
					if n.DeliveredAt == nil {
						undelivered = append(undelivered, n)
					}
				}
				list = undelivered
			}
			if list == nil {
				list = []notifications.Notification{}
			}
			if wantsJSON(cmd) {
				return writeJSON(cmd.OutOrStdout(), list)
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tAT\tKIND\tTITLE\tDELIVERED\tACKNOWLEDGED")
			for _, n := range list {
				delivered, acked := "no", ""
				if n.DeliveredAt != nil {
					delivered = "yes"
				}
				switch {
				case n.Waiting():
					acked = "waiting"
				case n.AcknowledgedAt != nil:
					acked = "yes"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", n.ID, n.At.Local().Format("Mon Jan 2 15:04"), n.Kind, n.Title, delivered, acked)
			}
			return tw.Flush()
		}),
	}
	cmd.Flags().BoolVar(&pending, "pending", false, "only those not delivered yet")
	cmd.Flags().BoolVar(&waiting, "waiting", false, "only those waiting for me to acknowledge them")
	cmd.Flags().BoolVar(&acknowledged, "acknowledged", false, "only those I've acknowledged, the last acknowledged last")
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
		Long: "Clicking one of the app's notifications opens what it's about: its link, when it has one, with the " +
			"system's default handler (open-url); else one about a run shows the run in its routine's window " +
			"(show-run), one about an update shows the app's updates (show-updates: Updates in the app's window), any other " +
			"about the app itself shows the app (show-app). " +
			"The frontend shows it; this opens nothing.",
		Annotations: readOnly(),
		Args:        cobra.ExactArgs(1),
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, args []string) error {
			n, err := notifications.Get(a.Layout.Notifications(), args[0])
			if err != nil {
				return err
			}
			out := ClickOutput{Notification: n.ID, Action: "show-app"}
			switch {
			case n.URL != "":
				out.Action, out.URL = "open-url", n.URL
			case n.Run != "":
				out.Action, out.Routine, out.Run, out.Hook = "show-run", n.Routine, n.Run, n.Hook
			case n.Update:
				out.Action = "show-updates"
			}
			return emit(cmd, out, strings.TrimSpace(out.Action+" "+out.Run+out.URL))
		}),
	}
	jsonFlag(cmd.Flags(), ClickOutput{})
	return cmd
}

// AckOutput is `notifications ack --json`: the notifications acknowledged
// (none for ones acknowledged already).
type AckOutput struct {
	Acknowledged []notifications.Notification `json:"acknowledged"`
}

func newNotificationsAckCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "ack [<id>...]",
		Short: "Acknowledge notifications waiting for it",
		Long: "Acknowledges notifications waiting for me to acknowledge them (`notifications list --waiting`): " +
			"they stop waiting, and once none waits, health no longer counts them. One acknowledged already stays so.",
		Args: func(_ *cobra.Command, args []string) error {
			if all == (len(args) > 0) {
				return fmt.Errorf("name the notifications to acknowledge, or pass --all")
			}
			return nil
		},
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, args []string) error {
			done, err := a.Acknowledge(args, all, time.Now())
			if err != nil {
				return err
			}
			titles := make([]string, len(done))
			for i, n := range done {
				titles[i] = n.Title
			}
			text := "Nothing was waiting."
			if len(done) > 0 {
				text = fmt.Sprintf("Acknowledged %d: %s", len(done), strings.Join(titles, "; "))
			}
			return emit(cmd, AckOutput{Acknowledged: done}, text)
		}),
	}
	cmd.Flags().BoolVar(&all, "all", false, "every notification waiting")
	jsonFlag(cmd.Flags(), AckOutput{})
	return cmd
}
