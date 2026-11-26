package cli

import (
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
)

// SkipOutput is `templates skip --json`.
type SkipOutput struct {
	Skipped []string `json:"skipped"`
}

// OverrideOutput is `templates override --json`.
type OverrideOutput struct {
	Overridden []engine.Overridden `json:"overridden"`
}

func newTemplatesOutdatedCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:         "outdated",
		Annotations: readOnly(),
		Short:       "List what I installed from a template that has changed since (--all: changes I didn't take too)",
		Long: "Lists each routine, program, hook and watcher installed from a template whose content changed since " +
			"(a newer app shipped another): its installed and new versions (a template's version is for reading; " +
			"its content tells whether it changed), what changed, and whether I edited my copy (edited: yes, no, or " +
			"unknown for one installed before installs were recorded). blocked says why one can't be overridden; " +
			"with, the watcher overridden with a hook. A change I chose not to take (`templates skip`) is listed " +
			"only with --all. The app offers these in a window after it updates.",
		Args: cobra.NoArgs,
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, _ []string) error {
			ups, err := a.TemplateUpdates(all)
			if err != nil {
				return err
			}
			if wantsJSON(cmd) {
				return writeJSON(cmd.OutOrStdout(), ups)
			}
			out := cmd.OutOrStdout()
			if len(ups) == 0 {
				fmt.Fprintln(out, "Everything installed from a template is up to date with it.")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tKIND\tTEMPLATE\tINSTALLED\tNEW\tEDITED\tNOTE")
			for _, u := range ups {
				var note []string
				if u.Skipped {
					note = append(note, "not taken")
				}
				if u.Blocked != "" {
					note = append(note, u.Blocked)
				}
				if len(u.With) > 0 {
					note = append(note, "with "+strings.Join(u.With, ", "))
				}
				if u.Changes != "" {
					note = append(note, oneLine(u.Changes, 60))
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", u.Name, u.Kind, u.Template, orDash(u.InstalledVersion),
					orDash(u.Version), u.Edited, orDash(strings.Join(note, "; ")))
			}
			_ = tw.Flush()
			fmt.Fprintln(out, "\nOverride: "+product.Command+" templates override <name>... (or --all); leave as they are: "+
				product.Command+" templates skip <name>... (or --all)")
			return nil
		}),
	}
	cmd.Flags().BoolVar(&all, "all", false, "include changes I chose not to take")
	jsonFlag(cmd.Flags(), []engine.TemplateUpdate{})
	return cmd
}

// pickUpdates is names, or with all every update offered that can be taken.
func pickUpdates(a *engine.App, names []string, all bool) ([]string, error) {
	switch {
	case all && len(names) > 0:
		return nil, errors.New("name some, or --all, not both")
	case !all && len(names) == 0:
		return nil, errors.New("name what to override, or --all")
	case len(names) > 0:
		return names, nil
	}
	ups, err := a.TemplateUpdates(false)
	if err != nil {
		return nil, err
	}
	for _, u := range ups {
		if u.Blocked == "" {
			names = append(names, u.Name)
		}
	}
	return names, nil
}

func newTemplatesOverrideCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "override [<name>...]",
		Short: "Replace what I installed from a template with its new version (`templates outdated` lists them)",
		Long: "Replaces each named routine, program, hook or watcher with its template's new version, filled in with " +
			"my parameter values (a new parameter takes its default), built again when it ships as source. Its name, " +
			"settings (on or off, schedule, parameter values) and memories are kept; my edits to it are not. One I " +
			"trusted stays trusted (choosing it is trusting the new version); one I hadn't stays untrusted. A hook's " +
			"watcher whose template changed too is overridden with it, first. --all overrides every one offered " +
			"(not those I chose not to take, nor blocked ones).",
		Args: cobra.ArbitraryArgs,
		RunE: withAppForEdit(func(cmd *cobra.Command, a *engine.App, args []string) error {
			names, err := pickUpdates(a, args, all)
			if err != nil {
				return err
			}
			if len(names) == 0 {
				return emit(cmd, OverrideOutput{Overridden: []engine.Overridden{}}, "Nothing to override.")
			}
			done, err := a.OverrideFromTemplates(cmd.Context(), names, cmd.ErrOrStderr())
			out := OverrideOutput{Overridden: done}
			if out.Overridden == nil {
				out.Overridden = []engine.Overridden{}
			}
			var text strings.Builder
			for _, o := range done {
				fmt.Fprintf(&text, "Overrode the %s %s", o.Kind, o.Name)
				if o.Version != "" {
					fmt.Fprintf(&text, " with version %s", o.Version)
				}
				if o.Trusted {
					text.WriteString(", still trusted.\n")
				} else {
					text.WriteString(", not trusted.\n")
				}
			}
			if err != nil {
				if len(done) > 0 {
					fmt.Fprint(cmd.OutOrStdout(), text.String())
				}
				return err
			}
			return emit(cmd, out, strings.TrimSuffix(text.String(), "\n"))
		}),
	}
	cmd.Flags().BoolVar(&all, "all", false, "override every one offered")
	jsonFlag(cmd.Flags(), OverrideOutput{})
	return cmd
}

func newTemplatesSkipCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "skip [<name>...]",
		Short: "Leave what I installed from a template as it is: its template's change isn't offered again, a later one is",
		Args:  cobra.ArbitraryArgs,
		RunE: withAppForEdit(func(cmd *cobra.Command, a *engine.App, args []string) error {
			names := args
			if all {
				if len(args) > 0 {
					return errors.New("name some, or --all, not both")
				}
				ups, err := a.TemplateUpdates(false)
				if err != nil {
					return err
				}
				for _, u := range ups {
					names = append(names, u.Name)
				}
			} else if len(names) == 0 {
				return errors.New("name what to leave as it is, or --all")
			}
			if err := a.SkipTemplateUpdates(names); err != nil {
				return err
			}
			if len(names) == 0 {
				return emit(cmd, SkipOutput{Skipped: []string{}}, "Nothing offered.")
			}
			return emit(cmd, SkipOutput{Skipped: names}, "Left as they are: "+strings.Join(names, ", ")+".")
		}),
	}
	cmd.Flags().BoolVar(&all, "all", false, "every one offered")
	jsonFlag(cmd.Flags(), SkipOutput{})
	return cmd
}
