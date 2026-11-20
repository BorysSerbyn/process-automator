package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newRoutinesPauseCmd is `routines pause|resume <routine>`: turns its
// schedule off or on (routines.<name>.enabled), so frontends never build the key.
func newRoutinesPauseCmd(resume bool) *cobra.Command {
	use, short, verb := "pause <routine>", "Stop a routine running on its schedule (run now still works)", "Paused"
	if resume {
		use, short, verb = "resume <routine>", "Let a paused routine run on its schedule again", "Resumed"
	}
	var asJSON bool
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := routineExists(cmd, args[0])
			if err != nil {
				return err
			}
			st, err := settingsStore(cmd)
			if err != nil {
				return err
			}
			if _, err := st.Set("routines."+r.Name+".enabled", fmt.Sprint(resume)); err != nil {
				return err
			}
			out := RoutineChange{Routine: r.Name, Path: r.Path, Enabled: &resume}
			return emit(cmd, out, fmt.Sprintf("%s %s.", verb, r.Name))
		},
	}
	jsonFlag(cmd.Flags(), &asJSON, RoutineChange{})
	return cmd
}
