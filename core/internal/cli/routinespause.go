package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
)

// newRoutinesPauseCmd is `routines pause|resume <routine>` (either kind):
// turns its schedule off or on (routines.<name>.enabled), so frontends never build the key.
func newRoutinesPauseCmd(resume bool) *cobra.Command {
	use, short, verb := "pause <routine>", "Stop a routine running on its schedule (run now still works)", "Paused"
	if resume {
		use, short, verb = "resume <routine>", "Let a paused routine run on its schedule again", "Resumed"
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := loadApp(cmd)
			if err != nil {
				return err
			}
			x, err := a.FindAny(args[0]) // a hook's name too
			if err != nil {
				return err
			}
			return setEnabled(cmd, x, resume, verb)
		},
	}
	jsonFlag(cmd.Flags(), RoutineChange{})
	return cmd
}

// setEnabled pauses or resumes a routine of either kind, or a hook.
func setEnabled(cmd *cobra.Command, x engine.Runnable, on bool, verb string) error {
	st, err := settingsStore(cmd)
	if err != nil {
		return err
	}
	if _, err := st.Set(enabledKey(x), fmt.Sprint(on)); err != nil {
		return err
	}
	_, hook := engine.AsHook(x)
	out := RoutineChange{Routine: x.Name(), Kind: x.Kind().Written(), Path: x.Path(), Enabled: &on, Hook: hook}
	return emit(cmd, out, fmt.Sprintf("%s %s.", verb, x.Name()))
}
