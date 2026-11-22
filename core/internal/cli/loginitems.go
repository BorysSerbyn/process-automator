package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/loginitems"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
)

// anyUI names the frontend in help, which is the same on every OS (the
// contracts are made from it); loginitems.UIName is this OS's, for messages.
const anyUI = "menu bar app or tray"

func newLoginItemsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login-items",
		Short: "Whether the app (its " + anyUI + " and the scheduler) starts at login; starting and stopping it now",
		Long: "status shows whether it starts at login; enable and disable turn that on or off, and neither starts " +
			"nor stops what's running now. stop stops the scheduler and the " + anyUI + " now (Quit in its menu " +
			"runs it); start starts the scheduler now (opening the app runs it). Neither changes starting at login.",
	}
	show := func(cmd *cobra.Command) error {
		items, err := loginitems.List(loginitems.Labels())
		if err != nil {
			return err
		}
		out := loginitems.Summarize(items)
		if wantsJSON(cmd) {
			return writeJSON(cmd.OutOrStdout(), out)
		}
		for _, it := range items {
			state := "starts at login"
			switch {
			case !it.Installed:
				state = "not installed (install the app first)"
			case !it.Enabled:
				state = "doesn't start at login"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%-9s %s\n", it.Name, state)
		}
		return nil
	}
	set := func(on bool) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, _ []string) error {
			if err := loginitems.Set(loginitems.Labels(), on); err != nil {
				return err
			}
			return show(cmd)
		}
	}
	withJSON := func(c *cobra.Command, out any) *cobra.Command {
		var asJSON bool
		jsonFlag(c.Flags(), &asJSON, out)
		return c
	}
	cmd.AddCommand(
		withJSON(&cobra.Command{Use: "status", Short: "Show whether the app starts at login", Annotations: readOnly(), Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error { return show(cmd) }}, loginitems.Status{}),
		withJSON(&cobra.Command{Use: "enable", Short: "Start the app at login", Args: cobra.NoArgs, RunE: set(true),
			Annotations: map[string]string{annNotReadOnly: "starting at login is mine"}}, loginitems.Status{}),
		withJSON(&cobra.Command{Use: "disable", Short: "Don't start the app at login (what's running keeps running)", Args: cobra.NoArgs, RunE: set(false),
			Annotations: map[string]string{annNotReadOnly: "starting at login is mine"}}, loginitems.Status{}),
		withJSON(&cobra.Command{
			Use:   "start",
			Short: "Start the scheduler now, if it isn't running (opening the app runs this)",
			Long: "Idempotent: a scheduler already running is left as it is. It starts the scheduler only: opening " +
				product.Name + " brings back its " + anyUI + ". Starting at login stays as it is.",
			Annotations: map[string]string{annNotReadOnly: "it starts the scheduler"},
			Args:        cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				out, err := loginitems.Start(loginitems.Labels())
				if err != nil {
					return err
				}
				text := "The scheduler is running."
				if out.Changed {
					text = "Started the scheduler."
				}
				return emit(cmd, out, text)
			},
		}, loginitems.RunningOutput{}),
		withJSON(&cobra.Command{
			Use:   "stop",
			Short: "Stop the scheduler and the " + anyUI + " now; both start again at the next login (Quit runs this)",
			Long: "Stops the scheduler, so no routine runs on its schedule until it's started again (`" + product.Command +
				" login-items start`, opening " + product.Name + ", or the next login), then the " + anyUI + " if one is " +
				"running. The process that ran this is left to exit on its own: Quit in the " + anyUI + "'s menu runs " +
				"it, then quits.",
			Annotations: map[string]string{annNotReadOnly: "it stops the scheduler"},
			Args:        cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				exe, err := os.Executable()
				if err == nil {
					exe, err = filepath.EvalSymlinks(exe)
				}
				if err != nil {
					return err
				}
				out, err := loginitems.Stop(loginitems.Labels(), filepath.Dir(exe), []int{os.Getpid(), os.Getppid()})
				if err != nil {
					return err
				}
				text := "The scheduler isn't running"
				if out.Changed {
					text = "Stopped the scheduler"
				}
				if out.UIStopped {
					text += " and the " + loginitems.UIName
				}
				return emit(cmd, out, text+"; both start again at the next login.")
			},
		}, loginitems.RunningOutput{}),
	)
	return cmd
}
