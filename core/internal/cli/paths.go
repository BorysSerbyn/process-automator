package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
)

// PathsOutput is `paths --json`: where the app keeps its files. Frontends
// read none of them: they ask the CLI (`status`, `notifications list`).
type PathsOutput struct {
	Config   string `json:"config"`
	Data     string `json:"data"`
	Routines string `json:"routines"`
	// Programs is the programs folder: the app's copies of the programs added.
	Programs string `json:"programs,omitempty"`
	// Hooks and Watchers are the hooks' and watchers' folders: the app's
	// copies of those added.
	Hooks     string `json:"hooks,omitempty"`
	Watchers  string `json:"watchers,omitempty"`
	RunStatus string `json:"runStatus"`
	History   string `json:"history"`
	// Notifications is the app's own notifications file.
	Notifications string `json:"notifications"`
	// MCPChanges logs every side-effecting call made through `mcp serve`.
	MCPChanges string `json:"mcpChanges"`
	// FrontendLog is the log a frontend keeps there, if it keeps one: the
	// Linux tray writes its own, and its windows', to it.
	FrontendLog string `json:"frontendLog"`
	// Templates is where the app's templates are (empty if unknown).
	Templates string `json:"templates,omitempty"`
}

// paths are the files frontends use.
func pathsOf(l engine.Layout) PathsOutput {
	p := PathsOutput{
		Config: l.Config(), Data: l.Data(), Routines: l.Routines(), Programs: l.Programs(), Hooks: l.Hooks(), Watchers: l.Watchers(), RunStatus: l.RunStatus(),
		History: l.History(), Notifications: l.Notifications(), MCPChanges: l.MCPChanges(),
		FrontendLog: l.FrontendLog(),
	}
	p.Templates, _ = engine.TemplatesDir()
	return p
}

func newPathsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "paths",
		Annotations: readOnly(),
		Short:       "Show where the app keeps its config, routines, programs, hooks, run status and history",
		Args:        cobra.NoArgs,
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, _ []string) error {
			p := pathsOf(a.Layout)
			if wantsJSON(cmd) {
				return writeJSON(cmd.OutOrStdout(), p)
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			for _, row := range [][2]string{
				{"config", p.Config}, {"data", p.Data}, {"routines", p.Routines}, {"programs", p.Programs}, {"hooks", p.Hooks}, {"watchers", p.Watchers},
				{"run status", p.RunStatus}, {"history", p.History}, {"notifications", p.Notifications}, {"mcp changes", p.MCPChanges},
				{"frontend log", p.FrontendLog},
				{"templates", orDash(p.Templates)},
			} {
				fmt.Fprintf(tw, "%s\t%s\n", row[0], row[1])
			}
			return tw.Flush()
		}),
	}
	jsonFlag(cmd.Flags(), PathsOutput{})
	return cmd
}
