package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// The app's login items (LaunchAgents on macOS, systemd user units on
// Linux), as the build names them (the Makefile's UI_LABEL and SCHED_LABEL).
var (
	uiLabel        = "dev.procauto.ui"
	schedulerLabel = "dev.procauto.scheduler"
)

// LoginItem is one thing that starts at login.
type LoginItem struct {
	// Name is ui (automator-ui: the menu bar indicator, settings, …) or scheduler.
	Name  string `json:"name"`
	Label string `json:"label"`
	// Installed is whether it's set up to start at login at all (the
	// install puts it there).
	Installed bool `json:"installed"`
	// Enabled is whether it starts at login.
	Enabled bool `json:"enabled"`
}

// LoginItemsOutput is `login-items --json`.
type LoginItemsOutput struct {
	// Installed is whether any item is set up to start at login (none before
	// the app is installed).
	Installed bool `json:"installed"`
	// Enabled is true when something is installed and every installed item
	// starts at login.
	Enabled bool        `json:"enabled"`
	Items   []LoginItem `json:"items"`
}

// loginItemLabels are the login items this build has (Linux builds have no
// automator-ui, so no ui label).
func loginItemLabels() map[string]string {
	labels := map[string]string{}
	for name, label := range map[string]string{"ui": uiLabel, "scheduler": schedulerLabel} {
		if label != "" {
			labels[name] = label
		}
	}
	return labels
}

func newLoginItemsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "login-items",
		Short: "Whether the app (automator-ui and the scheduler) starts at login",
		Long: "status shows it; enable and disable turn starting at login on or off. Neither starts nor stops " +
			"what's running now.",
	}
	jsonFlag(cmd.PersistentFlags(), &asJSON, LoginItemsOutput{})
	show := func(cmd *cobra.Command) error {
		items, err := loginItems(loginItemLabels())
		if err != nil {
			return err
		}
		out := summarizeLoginItems(items)
		if asJSON {
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
			if err := setLoginItems(loginItemLabels(), on); err != nil {
				return err
			}
			return show(cmd)
		}
	}
	cmd.AddCommand(
		&cobra.Command{Use: "status", Short: "Show whether the app starts at login", Annotations: readOnly(), Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error { return show(cmd) }},
		&cobra.Command{Use: "enable", Short: "Start the app at login", Args: cobra.NoArgs, RunE: set(true),
			Annotations: map[string]string{annNotForRoutines: "starting at login is mine"}},
		&cobra.Command{Use: "disable", Short: "Don't start the app at login (what's running keeps running)", Args: cobra.NoArgs, RunE: set(false),
			Annotations: map[string]string{annNotForRoutines: "starting at login is mine"}},
	)
	return cmd
}

func summarizeLoginItems(items []LoginItem) LoginItemsOutput {
	out := LoginItemsOutput{Enabled: true, Items: items}
	for _, it := range items {
		out.Installed = out.Installed || it.Installed
		if it.Installed && !it.Enabled {
			out.Enabled = false
		}
	}
	out.Enabled = out.Enabled && out.Installed
	return out
}
