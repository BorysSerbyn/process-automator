// Package cli defines the procauto command tree. The CLI is the backend API:
// people and any frontend call the same commands, using --json for stable output.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/pluginapi"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
	"github.com/BorysSerbyn/process-automator/core/internal/sysenv"
	"github.com/BorysSerbyn/process-automator/core/internal/version"
)

// NewRoot builds the root command with the installed plugins.
func NewRoot(plugins []pluginapi.Plugin) *cobra.Command {
	root := &cobra.Command{
		Use:           "procauto",
		Short:         "Scheduled LLM routines with tools from MCP servers",
		Version:       version.Version,
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	root.PersistentFlags().String("config", "", "config file (default: per-user config dir)")
	root.PersistentPreRun = func(cmd *cobra.Command, _ []string) { adoptLoginPath(cmd) }
	root.PersistentPostRun = func(cmd *cobra.Command, _ []string) { refreshSetupStatus(cmd) }
	root.AddCommand(newVersionCmd(), newAuthCmd(), newSettingsCmd(), newMCPCmd(), newRunCmd(), newDaemonCmd(), newRoutinesCmd(), newPathsCmd(), newUpdateCmd(), newRunsCmd(), newTemplatesCmd(), newPluginsCmd(), newStartCmd(), newStopCmd(), newScheduleCmd(), newUsageCmd(), newSetupCmd(), newNotificationsCmd(), newDoctorCmd(), newMemoryCmd(), newLoginItemsCmd(), newUninstallCmd())
	root.SetContext(withPlugins(context.Background(), plugins))
	return root
}

// Main runs the command line. An interrupt or termination cancels the
// command's context, so a running routine records how it ended instead of
// being cut off (e.g. when an MCP client cancels a call).
func Main(plugins []pluginapi.Plugin) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	root := NewRoot(plugins)
	root.SetContext(withPlugins(ctx, plugins))
	return execute(root)
}

// execute runs the command line; a command given --json that fails also
// prints the error as JSON, for frontends.
func execute(root *cobra.Command) error {
	cmd, err := root.ExecuteC()
	if err != nil && wantsJSON(cmd) && !errors.As(err, new(reportedError)) {
		_ = writeJSON(cmd.OutOrStdout(), ErrorOutput{Error: err.Error()})
	}
	return err
}

// ErrorOutput is what a command given --json prints on stdout when it fails
// (the error line still goes to stderr, for people).
type ErrorOutput struct {
	Error string `json:"error"`
}

// reportedError is a failure the command's output already describes (a
// check that found problems): --json prints no error object for it.
type reportedError struct{ error }

func (e reportedError) Unwrap() error { return e.error }

func reported(err error) error { return reportedError{err} }

func wantsJSON(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	on, err := cmd.Flags().GetBool("json")
	return err == nil && on
}

// useLoginPath turns adoptLoginPath on (tests turn it off: they never run the
// login shell).
var useLoginPath = true

// adoptLoginPath gives every command the user's PATH as their login shell has
// it, saved by the scheduler (or resolved once here, when there's none and the
// command wasn't started from a terminal), so commands an app runs find
// claude and MCP server commands as a terminal does. The scheduler resolves
// its own at startup. A failure only warns, in a terminal: an app reading the
// command's error mustn't get this instead.
func adoptLoginPath(cmd *cobra.Command) {
	if !useLoginPath || cmd.Name() == "daemon" {
		return
	}
	path, err := configPath(cmd)
	if err != nil {
		return
	}
	interactive := isTerminal(os.Stdin)
	if err := sysenv.UseSavedLoginShellPath(engine.LayoutOf(path).LoginPath(), !interactive); err != nil && interactive {
		fmt.Fprintln(os.Stderr, "warning: login shell PATH:", err)
	}
}

// changesSetup are the commands whose success can change what setup has
// left: run status says so at once, not on the scheduler's next pass.
var changesSetup = map[string]bool{
	"auth login": true, "auth logout": true, "settings set": true, "settings reset": true,
	"templates install": true, "routines create": true, "routines update": true, "routines delete": true,
	"routines trust": true, "routines pause": true, "routines resume": true,
	"plugins add": true, "plugins remove": true, "usage statusline install": true, "usage statusline uninstall": true,
}

// refreshSetupStatus records the setup steps left in run status after a
// command that changes them (it only runs after a command succeeds).
func refreshSetupStatus(cmd *cobra.Command) {
	path := strings.TrimPrefix(cmd.CommandPath(), cmd.Root().Name()+" ")
	if !changesSetup[path] {
		return
	}
	a, err := loadApp(cmd)
	if err != nil {
		return
	}
	left, err := setupLeft(a)
	if err != nil {
		return
	}
	_ = runstatus.SetSetup(a.Layout.RunStatus(), left, time.Now())
}
