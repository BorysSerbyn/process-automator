package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// ToolInfo is one row of `mcp tools --json`.
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema,omitempty"`
	// ReadOnly is the server's readOnlyHint annotation (nil when not declared).
	ReadOnly *bool `json:"readOnly,omitempty"`
}

func newMCPCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "mcp", Short: "Inspect configured MCP servers, serve this CLI as one, and connect it to Claude Code"}
	var asJSON bool
	tools := &cobra.Command{
		Use:         "tools <server>",
		Annotations: readOnly(),
		Short:       "List the tools a server exposes",
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := loadApp(cmd)
			if err != nil {
				return err
			}
			session, err := a.Connector.Connect(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			defer session.Close()
			res, err := session.ListTools(cmd.Context(), nil)
			if err != nil {
				return err
			}
			rows := make([]ToolInfo, 0, len(res.Tools))
			for _, t := range res.Tools {
				row := ToolInfo{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema}
				if t.Annotations != nil {
					ro := t.Annotations.ReadOnlyHint
					row.ReadOnly = &ro
				}
				rows = append(rows, row)
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), rows)
			}
			for _, r := range rows {
				desc := strings.Join(strings.Fields(r.Description), " ")
				if len(desc) > 100 {
					desc = desc[:97] + "..."
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%-32s %s\n", r.Name, desc)
			}
			return nil
		},
	}
	jsonFlag(tools.Flags(), &asJSON, []ToolInfo{})

	call := &cobra.Command{
		Use:     "call <server> <tool> [json-args]",
		Short:   "Call one tool and print its result (debugging; runs writes for real)",
		Example: "  " + product.Command + ` mcp call automator runs '{"since":"7d"}'`,
		Args:    cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			var params map[string]any
			if len(args) == 3 {
				if err := json.Unmarshal([]byte(args[2]), &params); err != nil {
					return fmt.Errorf("json-args: %w", err)
				}
			}
			a, err := loadApp(cmd)
			if err != nil {
				return err
			}
			session, err := a.Connector.Connect(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			defer session.Close()
			res, err := session.CallTool(cmd.Context(), &sdk.CallToolParams{Name: args[1], Arguments: params})
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), pluginapi.ResultText(res))
			if res.IsError {
				return fmt.Errorf("%s returned an error", args[1])
			}
			return nil
		},
	}
	cmd.AddCommand(tools, call, newMCPServeCmd(), newMCPConnectCmd())
	return cmd
}
