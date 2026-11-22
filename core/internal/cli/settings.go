package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/hooks"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/programs"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/watchers"
)

// settingsStore includes the per-routine settings of every routine on this
// device, of either kind.
func settingsStore(cmd *cobra.Command) (settings.Store, error) {
	path, err := configPath(cmd)
	if err != nil {
		return settings.Store{}, err
	}
	layout := engine.LayoutOf(path)
	set, err := routines.Load(layout.Routines())
	if err != nil {
		return settings.Store{}, err
	}
	progs, err := programs.Load(layout.Programs())
	if err != nil {
		return settings.Store{}, err
	}
	for _, e := range set.Errors {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: skipping %v\n", e)
	}
	for _, e := range progs.Errors {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: skipping %v\n", e)
	}
	st := settings.Store{Path: path}
	for _, r := range set.Routines {
		st.Extra = append(st.Extra, r.Settings()...)
	}
	for _, p := range progs.Programs {
		st.Extra = append(st.Extra, p.Settings()...)
	}
	// Hooks' and watchers' own: hooks.<hook>.<field>, watchers.<watcher>.enabled.
	if hs, err := hooks.Load(layout.Hooks()); err == nil {
		for _, h := range hs.Hooks {
			st.Extra = append(st.Extra, h.Settings()...)
		}
	}
	if ws, err := watchers.Load(layout.Watchers()); err == nil {
		for _, w := range ws.Watchers {
			st.Extra = append(st.Extra, settings.WatcherSettings(w.Name)...)
		}
	}
	return st, nil
}

// settingScope is the scope a setting's key is in: routines (a routine's),
// hooks (a hook's or a watcher's) or app.
func settingScope(key string) string {
	switch {
	case strings.HasPrefix(key, "routines."):
		return "routines"
	case strings.HasPrefix(key, "hooks."), strings.HasPrefix(key, "watchers."):
		return "hooks"
	default:
		return "app"
	}
}

// programKey refuses an LLM routine's setting (routines.<program>.effort)
// for a program, saying so, rather than calling the key unknown.
func programKey(cmd *cobra.Command, key string) error {
	rest, ok := strings.CutPrefix(key, "routines.")
	if !ok {
		return nil
	}
	name, field, _ := strings.Cut(rest, ".")
	field, _, _ = strings.Cut(field, ".")
	path, err := configPath(cmd)
	if err != nil || !programs.Exists(engine.LayoutOf(path).Programs(), name) || slices.Contains(settings.ProgramFields, field) {
		return nil
	}
	return fmt.Errorf("%s is a program: %s is an LLM routine's setting (a program's are %s)", name, field, strings.Join(settings.ProgramFields, ", "))
}

func newSettingsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "settings",
		Short: "Read and change settings (see contracts/settings.schema.json)",
	}
	var scope, routine string

	list := &cobra.Command{
		Use:         "list",
		Annotations: readOnly(),
		Short:       "List every setting with its value, default and description",
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !slices.Contains([]string{"all", "app", "routines", "hooks"}, scope) {
				return fmt.Errorf("unknown --scope %q: all, app, routines or hooks", scope)
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
				a, err := loadApp(cmd)
				if err != nil {
					return err
				}
				x, err := a.FindAny(routine) // a hook's too
				if err != nil {
					return err
				}
				prefix := strings.TrimSuffix(enabledKey(x), "enabled")
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
					if settingScope(s.Key) == scope {
						kept = append(kept, s)
					}
				}
				all = kept
			}
			if wantsJSON(cmd) {
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
	list.Flags().StringVar(&scope, "scope", "all", "all, app (app-wide settings), routines (each routine's) or hooks (each hook's and watcher's)")
	list.Flags().StringVar(&routine, "routine", "", "only this routine's (or hook's) settings")
	get := &cobra.Command{
		Use:         "get <key>",
		Annotations: readOnly(),
		Short:       "Print one setting's value",
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := programKey(cmd, args[0]); err != nil {
				return err
			}
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
			if err := programKey(cmd, args[0]); err != nil {
				return err
			}
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
			if err := programKey(cmd, args[0]); err != nil {
				return err
			}
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
	jsonFlag(cmd.PersistentFlags(), settings.State{})
	jsonFlag(list.Flags(), []settings.State{})
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
