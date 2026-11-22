package cli

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
)

func newDaemonCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "daemon",
		Short: "Run routines on their schedules (started at login by the scheduler LaunchAgent)",
		Long: "Runs in the foreground and checks every 30s for due routines. Config, settings and routine " +
			"files are re-read on every check, so changes apply without a restart. Only one daemon runs at a time.",
		Args: cobra.NoArgs,
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return a.Daemon(ctx)
		}),
	}
}
