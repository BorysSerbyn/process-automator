package cli

import (
	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/product"
)

// VersionOutput is `version --json`.
type VersionOutput struct {
	Version string `json:"version"`
}

func newVersionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "version",
		Annotations: readOnly(),
		Short:       "Print the version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return emit(cmd, VersionOutput{Version: product.Version}, product.Version)
		},
	}
	jsonFlag(cmd.Flags(), VersionOutput{})
	return cmd
}
