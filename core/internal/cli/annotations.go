package cli

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Command annotations say what a command does to the world. Frontends that
// expose commands generically (the MCP server) read them; a command that
// declares nothing is treated as side-effecting.
const (
	// annReadOnly marks a command with no side effects (reads only).
	annReadOnly = "procauto/read-only"
	// annReadOnlyWith names a bool flag that makes the command read-only
	// (run --dry-run, update --check).
	annReadOnlyWith = "procauto/read-only-with"
	// annNotForRoutines keeps a command from routines (the automator server
	// inside a run), with the reason.
	annNotForRoutines = "procauto/not-for-routines"
)

// readOnly is the annotation set of a command with no side effects.
func readOnly() map[string]string { return map[string]string{annReadOnly: "true"} }

func isReadOnly(c *cobra.Command) bool { return c.Annotations[annReadOnly] == "true" }

// annNotForMCP marks a flag that frontends exposing commands generically
// (the MCP server) leave out, e.g. one that reads a local file.
const annNotForMCP = "procauto/not-for-mcp"

func hideFromMCP(c *cobra.Command, flag string) {
	_ = c.Flags().SetAnnotation(flag, annNotForMCP, []string{"true"})
}

func hiddenFromMCP(f *pflag.Flag) bool { return len(f.Annotations[annNotForMCP]) > 0 }

// Flag annotations for generic frontends:
const (
	// annViaStdin: send this flag's value on stdin through the named flag
	// set to "-" (a routine's --source goes as --from -: no size limits).
	annViaStdin = "procauto/via-stdin"
	// annRedact: keep this flag's values (name=value pairs) out of logs.
	annRedact = "procauto/redact"
)
