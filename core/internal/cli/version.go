package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/version"
)

// VersionOutput is `version --json`.
type VersionOutput struct {
	Version string `json:"version"`
}

func newVersionCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:         "version",
		Annotations: readOnly(),
		Short:       "Print the version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(VersionOutput{Version: version.Version})
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), version.Version)
			return err
		},
	}
	jsonFlag(cmd.Flags(), &asJSON, VersionOutput{})
	return cmd
}
