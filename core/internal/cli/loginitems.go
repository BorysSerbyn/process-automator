package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/product"
)

// LoginItem is one thing that starts at login.
type LoginItem struct {
	// Name is ui (macOS's menu bar app, automator-ui; Linux's tray) or scheduler.
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

// RunningOutput is `login-items start --json` and `login-items stop --json`:
// whether the scheduler runs now, and what the command changed.
type RunningOutput struct {
	// Scheduler is running or stopped, as the command left it.
	Scheduler string `json:"scheduler" jsonschema:"enum=running,enum=stopped"`
	// Changed is whether the command started or stopped it (false: it
	// already was).
	Changed bool `json:"changed"`
	// UIStopped is whether stop stopped a running menu bar app or tray
	// (besides the one that ran it, if one did).
	UIStopped bool `json:"uiStopped,omitempty"`
}

// anyUI names the frontend in help, which is the same on every OS (the
// contracts are made from it); uiName is this OS's, for messages.
const anyUI = "menu bar app or tray"

const (
	schedulerRunning = "running"
	schedulerStopped = "stopped"
)

func newLoginItemsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login-items",
		Short: "Whether the app (its " + anyUI + " and the scheduler) starts at login; starting and stopping it now",
		Long: "status shows whether it starts at login; enable and disable turn that on or off, and neither starts " +
			"nor stops what's running now. stop stops the scheduler and the " + anyUI + " now (Quit in its menu " +
			"runs it); start starts the scheduler now (opening the app runs it). Neither changes starting at login.",
	}
	show := func(cmd *cobra.Command) error {
		items, err := loginItems(loginItemLabels())
		if err != nil {
			return err
		}
		out := summarizeLoginItems(items)
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
			if err := setLoginItems(loginItemLabels(), on); err != nil {
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
			RunE: func(cmd *cobra.Command, _ []string) error { return show(cmd) }}, LoginItemsOutput{}),
		withJSON(&cobra.Command{Use: "enable", Short: "Start the app at login", Args: cobra.NoArgs, RunE: set(true),
			Annotations: map[string]string{annNotReadOnly: "starting at login is mine"}}, LoginItemsOutput{}),
		withJSON(&cobra.Command{Use: "disable", Short: "Don't start the app at login (what's running keeps running)", Args: cobra.NoArgs, RunE: set(false),
			Annotations: map[string]string{annNotReadOnly: "starting at login is mine"}}, LoginItemsOutput{}),
		withJSON(&cobra.Command{
			Use:   "start",
			Short: "Start the scheduler now, if it isn't running (opening the app runs this)",
			Long: "Idempotent: a scheduler already running is left as it is. It starts the scheduler only: opening " +
				product.Name + " brings back its " + anyUI + ". Starting at login stays as it is.",
			Annotations: map[string]string{annNotReadOnly: "it starts the scheduler"},
			Args:        cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				out, err := startLoginItems(loginItemLabels())
				if err != nil {
					return err
				}
				text := "The scheduler is running."
				if out.Changed {
					text = "Started the scheduler."
				}
				return emit(cmd, out, text)
			},
		}, RunningOutput{}),
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
				out, err := stopLoginItems(loginItemLabels(), filepath.Dir(exe), []int{os.Getpid(), os.Getppid()})
				if err != nil {
					return err
				}
				text := "The scheduler isn't running"
				if out.Changed {
					text = "Stopped the scheduler"
				}
				if out.UIStopped {
					text += " and the " + uiName
				}
				return emit(cmd, out, text+"; both start again at the next login.")
			},
		}, RunningOutput{}),
	)
	return cmd
}

// startLoginItems starts the scheduler if it isn't running.
func startLoginItems(labels map[string]string) (RunningOutput, error) {
	label := labels["scheduler"]
	running, err := isSchedulerRunning(label)
	if err != nil {
		return RunningOutput{}, err
	}
	if running {
		return RunningOutput{Scheduler: schedulerRunning}, nil
	}
	if err := startScheduler(label); err != nil {
		return RunningOutput{}, err
	}
	return RunningOutput{Scheduler: schedulerRunning, Changed: true}, nil
}

// stopLoginItems stops the scheduler, then the frontends running from dir
// (the app's binaries' folder), except the processes spared (this one, and
// the one that ran it). Both are tried; the errors are joined.
func stopLoginItems(labels map[string]string, dir string, spare []int) (RunningOutput, error) {
	out := RunningOutput{Scheduler: schedulerStopped}
	var errs []error
	label := labels["scheduler"]
	running, err := isSchedulerRunning(label)
	switch {
	case err != nil:
		errs = append(errs, err)
	case running:
		if err := stopScheduler(label); err != nil {
			out.Scheduler = schedulerRunning
			errs = append(errs, err)
		} else {
			out.Changed = true
		}
	}
	stopped, err := stopFrontends(labels["ui"], dir, spare)
	out.UIStopped = stopped
	if err != nil {
		errs = append(errs, err)
	}
	return out, errors.Join(errs...)
}

// spared is whether pid is one of spare.
func spared(pid int, spare []int) bool {
	for _, p := range spare {
		if p == pid {
			return true
		}
	}
	return false
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
