package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/spf13/cobra"
)

// PathsOutput is `paths --json`: where the app keeps its files. Frontends
// read none of them: they ask the CLI (`status`, `notifications list`).
type PathsOutput struct {
	Config    string `json:"config"`
	Data      string `json:"data"`
	Routines  string `json:"routines"`
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
		Config: l.Config(), Data: l.Data(), Routines: l.Routines(), RunStatus: l.RunStatus(),
		History: l.History(), Notifications: l.Notifications(), MCPChanges: l.MCPChanges(),
		FrontendLog: l.FrontendLog(),
	}
	p.Templates, _ = templatesDir()
	return p
}

func newPathsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:         "paths",
		Annotations: readOnly(),
		Short:       "Show where the app keeps its config, routines, run status and history",
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := loadApp(cmd)
			if err != nil {
				return err
			}
			p := pathsOf(a.Layout)
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), p)
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			for _, row := range [][2]string{
				{"config", p.Config}, {"data", p.Data}, {"routines", p.Routines},
				{"run status", p.RunStatus}, {"history", p.History}, {"notifications", p.Notifications}, {"mcp changes", p.MCPChanges},
				{"frontend log", p.FrontendLog},
				{"templates", orDash(p.Templates)},
			} {
				fmt.Fprintf(tw, "%s\t%s\n", row[0], row[1])
			}
			return tw.Flush()
		},
	}
	jsonFlag(cmd.Flags(), &asJSON, PathsOutput{})
	return cmd
}
