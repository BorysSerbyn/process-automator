package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"

	"github.com/BorysSerbyn/process-automator/core/internal/product"
)

func newUninstallCmd() *cobra.Command {
	var purge, yes bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the app, its CLI link and its login items; --purge also your config, routines, history, logs and secrets",
		Long: "Stops the scheduler and automator-ui, removes their login items, the " + product.Command + " link and the app. " +
			"Your config, routines, run history, logs and secrets stay unless you add --purge. Asks first; " +
			"without a terminal it needs --yes. An app installed with Homebrew is removed with brew.",
		Annotations: map[string]string{annNotReadOnly: "removing the app is mine"},
		Args:        cobra.NoArgs,
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, _ []string) error {
			root, installer, err := engine.InstalledApp()
			if err != nil {
				return err
			}
			what := "the app (" + root + "), its " + product.Command + " link and its login items"
			if purge {
				what += ", and your config, routines, history, logs and secrets (" + a.Layout.Data() + ")"
			}
			if !yes {
				if !isTerminal(os.Stdin) {
					return errors.New("uninstalling removes " + what + ": add --yes to go ahead")
				}
				if !newPrompt(cmd).Confirm("This removes "+what+". Go ahead?", false) {
					return errors.New("nothing removed")
				}
			}
			if err := engine.UninstallClaudeStatusLine(a); err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning: putting back your Claude Code status line:", err)
			}
			args := []string{installer, "uninstall", "--config", a.Layout.Config()}
			if purge {
				args = append(args, "--purge")
			}
			c := exec.CommandContext(cmd.Context(), "bash", args...)
			c.Stdout, c.Stderr = cmd.OutOrStdout(), cmd.ErrOrStderr()
			return c.Run()
		}),
	}
	cmd.Flags().BoolVar(&purge, "purge", false, "also remove config, routines, history, logs and secrets")
	cmd.Flags().BoolVar(&yes, "yes", false, "don't ask (needed without a terminal)")
	return cmd
}
