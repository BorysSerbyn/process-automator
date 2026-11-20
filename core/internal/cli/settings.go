package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
)

// settingsStore includes the per-routine settings of every routine file on this device.
func settingsStore(cmd *cobra.Command) (settings.Store, error) {
	path, err := configPath(cmd)
	if err != nil {
		return settings.Store{}, err
	}
	set, err := routines.Load(engine.LayoutOf(path).Routines())
	if err != nil {
		return settings.Store{}, err
	}
	warnBroken(cmd, set)
	st := settings.Store{Path: path}
	for _, r := range set.Routines {
		st.Extra = append(st.Extra, r.Settings()...)
	}
	return st, nil
}

func newSettingsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "settings",
		Short: "Read and change settings (see contracts/settings.schema.json)",
	}
	var asJSON bool
	var scope, routine string

	list := &cobra.Command{
		Use:         "list",
		Annotations: readOnly(),
		Short:       "List every setting with its value, default and description",
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !slices.Contains([]string{"all", "app", "routines"}, scope) {
				return fmt.Errorf("unknown --scope %q: all, app or routines", scope)
			}
			if routine != "" && cmd.Flags().Changed("scope") {
				return errors.New("--routine already picks one routine's settings: leave out --scope")
			}
			st, err := settingsStore(cmd)
			if err != nil {
				return err
			}
			all, err := st.List()
			if err != nil {
				return err
			}
			if routine != "" {
				if _, err := routineExists(cmd, routine); err != nil {
					return err
				}
				prefix := "routines." + routine + "."
				kept := all[:0]
				for _, s := range all {
					if strings.HasPrefix(s.Key, prefix) {
						kept = append(kept, s)
					}
				}
				all = kept
			} else if scope != "all" {
				kept := all[:0]
				for _, s := range all {
					if routine := strings.HasPrefix(s.Key, "routines."); (scope == "routines") == routine {
						kept = append(kept, s)
					}
				}
				all = kept
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), all)
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "KEY\tVALUE\tDEFAULT\tDESCRIPTION")
			for _, s := range all {
				def := ""
				if !s.IsDefault {
					def = format(s.Default, s.Setting)
				}
				desc := s.Description
				if s.NotImplemented {
					desc = notImplemented + " " + desc
				}
				if s.Inactive != "" {
					desc = "[" + s.Inactive + "] " + desc
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.Key, format(s.Value, s.Setting), def, desc)
			}
			return tw.Flush()
		},
	}
	list.Flags().StringVar(&scope, "scope", "all", "all, app (app-wide settings) or routines (each routine's)")
	list.Flags().StringVar(&routine, "routine", "", "only this routine's settings")
	get := &cobra.Command{
		Use:         "get <key>",
		Annotations: readOnly(),
		Short:       "Print one setting's value",
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := settingsStore(cmd)
			if err != nil {
				return err
			}
			s, err := st.Get(args[0])
			if err != nil {
				return err
			}
			return printState(cmd, s)
		},
	}
	set := &cobra.Command{
		Use:     "set <key> <value>",
		Short:   "Change a setting (validated against its type and limits)",
		Example: "  " + product.Command + " settings set llm.backend claude-code\n  " + product.Command + " settings set routines.standup.enabled false",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := settingsStore(cmd)
			if err != nil {
				return err
			}
			s, err := st.Set(args[0], args[1])
			if err != nil {
				return err
			}
			return printState(cmd, s)
		},
	}
	reset := &cobra.Command{
		Use:   "reset <key>",
		Short: "Remove an override so the default applies",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := settingsStore(cmd)
			if err != nil {
				return err
			}
			s, err := st.Reset(args[0])
			if err != nil {
				return err
			}
			return printState(cmd, s)
		},
	}
	// get, set and reset print one setting; list (its own --json) prints them all.
	jsonFlag(cmd.PersistentFlags(), &asJSON, settings.State{})
	jsonFlag(list.Flags(), &asJSON, []settings.State{})
	cmd.AddCommand(list, get, set, reset)
	return cmd
}

// notImplemented labels settings declared ahead of their feature.
const notImplemented = "[not implemented]"

func printState(cmd *cobra.Command, s settings.State) error {
	if s.NotImplemented {
		fmt.Fprintf(cmd.ErrOrStderr(), "note: %s is %s yet; it has no effect\n", s.Key, notImplemented)
	}
	return emit(cmd, s, s.Key+" = "+format(s.Value, s.Setting))
}

func format(v any, s settings.Setting) string {
	if v == nil {
		return "(not set)"
	}
	if s.Type == settings.TypePercent {
		return fmt.Sprintf("%g%%", v)
	}
	return fmt.Sprint(v)
}

// routineExists checks a routine is installed (settings for one that isn't
// would never be used).
func routineExists(cmd *cobra.Command, name string) (routines.Routine, error) {
	a, err := loadApp(cmd)
	if err != nil {
		return routines.Routine{}, err
	}
	set, err := routines.Load(a.Layout.Routines())
	if err != nil {
		return routines.Routine{}, err
	}
	return set.Find(name)
}
