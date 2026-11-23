package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/templates"
)

// InstallOutput is `templates install --json`.
type InstallOutput struct {
	Template string `json:"template"`
	// Kind is what it installed: a routine, a program, a hook or a watcher.
	Kind string `json:"kind" jsonschema:"enum=routine,enum=program,enum=hook,enum=watcher"`
	// Routine is the name it's installed as (a routine's, program's, hook's
	// or watcher's), and Path its file.
	Routine string `json:"routine"`
	Path    string `json:"path"`
	// Also is what was installed with it: a hook's watcher, from its
	// template, when it wasn't there.
	Also []engine.Added `json:"also,omitempty"`
}

func newTemplatesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "templates",
		Short: "Templates shipped with the app (routines, programs, hooks, watchers), to install as your own (as is, or to adapt)",
	}
	cmd.AddCommand(newTemplatesListCmd(), newTemplatesInstallCmd())
	return cmd
}

func newTemplatesListCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:         "list",
		Annotations: readOnly(),
		Short:       "List templates for this OS (--all for every OS); a hook comes with its watcher, as one",
		Long: "Lists the templates to install. A hook template and the watcher template it listens to are one " +
			"entry, the hook's: withWatcher is the watcher installed with it (while it isn't installed, its parameters " +
			"are among the hook's), and that watcher template isn't listed on its own; `templates install <watcher>` " +
			"still installs it alone.",
		Args: cobra.NoArgs,
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, _ []string) error {
			rows, err := a.Templates(all)
			if err != nil {
				return err
			}
			if wantsJSON(cmd) {
				return writeJSON(cmd.OutOrStdout(), rows)
			}
			out := cmd.OutOrStdout()
			if len(rows) == 0 {
				fmt.Fprintf(out, "No templates for %s.\n", routines.Platform())
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "TEMPLATE\tKIND\tPARAMETERS\tPLATFORMS\tBACKENDS\tINSTALLED\tDESCRIPTION")
			for _, r := range rows {
				var params []string
				for _, p := range r.Params {
					if p.Required {
						params = append(params, p.Name+"*")
					} else {
						params = append(params, p.Name)
					}
				}
				platforms := strings.Join(r.Platforms, ",")
				if platforms == "" {
					platforms = "all"
				}
				backends := strings.Join(r.Backends, ",")
				if backends == "" {
					backends = "all"
				}
				if r.Kind != templates.KindRoutine {
					backends = "-"
				}
				description := oneLine(r.Description, 60)
				if w := r.WithWatcher; w != nil && !w.Installed {
					description += " (comes with the watcher " + w.Name + ")"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%v\t%s\n", r.ID, r.Kind, orDash(strings.Join(params, ", ")), platforms, backends, r.Installed, description)
			}
			_ = tw.Flush()
			fmt.Fprintln(out, "\n* required. Install with: "+product.Command+" templates install <template> [--as <name>] [--set param=value]")
			return nil
		}),
	}
	cmd.Flags().BoolVar(&all, "all", false, "include templates for other operating systems")
	jsonFlag(cmd.Flags(), []engine.TemplateInfo{})
	return cmd
}

func newTemplatesInstallCmd() *cobra.Command {
	var as string
	var sets []string
	cmd := &cobra.Command{
		Use:   "install <template>",
		Short: "Install a template: a routine, a program, a hook or a watcher; it starts off and untrusted",
		Long: "A routine template is copied into the routines folder (never over an existing routine), its " +
			"parameter values stored as routines.<name>.params.<param> settings. A program, hook or watcher template is " +
			"added as `routines create --kind program`, `hooks add` or `watchers add` would add it, its parameter values " +
			"filled into its description file, and its executable built first when it ships as source (the build's " +
			"toolchain must be installed). A hook's watcher is installed with it from its template when it isn't there. " +
			"Each starts off and untrusted (turn it on with `routines trust <name> --resume`, `hooks trust <name> --resume`). " +
			"Values come from --set, then the template's defaults; required ones without a value are asked for (or an " +
			"error, without a terminal).",
		Example: "  " + product.Command + " templates install projects/acme/standup\n" +
			"  " + product.Command + " templates install personal/digest --set lookback=72h",
		Args: cobra.ExactArgs(1),
		RunE: withAppForEdit(func(cmd *cobra.Command, a *engine.App, args []string) error {
			plan, err := a.PlanTemplate(args[0])
			if err != nil {
				return err
			}
			t := plan.Template
			name := t.Name
			if as != "" {
				name = as
			}
			if !routines.ValidName(name) {
				return fmt.Errorf("%q can't name a %s: lowercase letters, digits and dashes", name, t.Kind)
			}
			values, err := paramValues(plan.Params, name, sets, cmd.InOrStdin(), cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			if t.Kind != templates.KindRoutine {
				return installFolderTemplate(cmd, a, plan, name, values)
			}
			// Settings first (disabled, parameter values), the file last: the
			// scheduler never sees a half-installed or enabled routine.
			rdir := a.Layout.Routines()
			r := t.Routine
			r.Name = name
			if _, err = disableNew(a, name, r.Settings()); err != nil {
				return err
			}
			undo := func() { _ = (settings.Store{Path: a.Layout.Config()}).ResetPrefix("routines." + name + ".") }
			st := settings.Store{Path: a.Layout.Config(), Extra: r.Settings()}
			for _, p := range t.Params {
				v, ok := values[p.Name]
				if !ok {
					continue
				}
				if _, err := st.Set("routines."+name+".params."+p.Name, v); err != nil {
					undo()
					return err
				}
			}
			path, err := templates.Copy(t, rdir, name)
			if err != nil {
				undo()
				return err
			}
			if wantsJSON(cmd) {
				return writeJSON(cmd.OutOrStdout(), InstallOutput{Template: t.ID, Kind: t.Kind, Routine: name, Path: path})
			}
			if t.Description != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", t.ID, t.Description)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Installed %s as %q (%s), paused and not trusted.\nTry it: "+product.Command+" run %s --dry-run\n"+
				"Turn it on (trust it and run it on its schedule): "+product.Command+" routines trust %s --resume\n",
				t.ID, name, path, name, name)
			return nil
		}),
	}
	cmd.Flags().StringVar(&as, "as", "", "routine name (default: the template's name)")
	cmd.Flags().StringArrayVar(&sets, "set", nil, "a parameter value, param=value (repeatable)")
	jsonFlag(cmd.Flags(), InstallOutput{})
	return cmd
}

// paramValues validates --set values and asks for required parameters that
// have none, before anything is written.
func paramValues(params []routines.Param, routine string, sets []string, in io.Reader, prompt io.Writer) (map[string]string, error) {
	byName := map[string]routines.Param{}
	for _, p := range params {
		byName[p.Name] = p
	}
	values := map[string]string{}
	for _, kv := range sets {
		k, v, ok := strings.Cut(kv, "=")
		p, known := byName[k]
		if !ok || !known {
			return nil, fmt.Errorf("--set %q: want param=value, with param one of the template's parameters", kv)
		}
		if _, err := p.Setting(routine).Parse(v); err != nil {
			return nil, err
		}
		values[k] = v
	}
	var missing []routines.Param
	for _, p := range params {
		if _, set := values[p.Name]; !set && p.Required {
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		return values, nil
	}
	if f, ok := in.(*os.File); !ok || !isTerminal(f) {
		var names []string
		for _, p := range missing {
			names = append(names, p.Name)
		}
		return nil, fmt.Errorf("required parameters without a value: %s (use --set param=value)", strings.Join(names, ", "))
	}
	r := bufio.NewReader(in)
	for _, p := range missing {
		for {
			fmt.Fprintf(prompt, "%s (%s", p.Name, p.Type)
			if p.Description != "" {
				fmt.Fprintf(prompt, ": %s", p.Description)
			}
			fmt.Fprint(prompt, "): ")
			line, err := r.ReadString('\n')
			if err != nil && line == "" {
				return nil, err
			}
			v := strings.TrimSpace(line)
			if _, err := p.Setting(routine).Parse(v); err != nil {
				fmt.Fprintln(prompt, err)
				continue
			}
			values[p.Name] = v
			break
		}
	}
	return values, nil
}

// isTerminal reports whether f is an interactive terminal: not a file, pipe
// or /dev/null (what commands an app starts get as stdin).
func isTerminal(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// installFolderTemplate installs a program, hook or watcher template (and a
// hook's watcher with it), saying what it added and how to turn it on. A
// build's output goes to stderr.
func installFolderTemplate(cmd *cobra.Command, a *engine.App, plan engine.TemplatePlan, name string, values map[string]string) error {
	t := plan.Template
	if t.Build != nil && !wantsJSON(cmd) {
		fmt.Fprintf(cmd.ErrOrStderr(), "Building %s (%s)…\n", t.ID, strings.Join(t.Build.Command, " "))
	}
	added, err := a.InstallFolderTemplate(cmd.Context(), plan, name, values, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	last := added[len(added)-1]
	out := InstallOutput{Template: t.ID, Kind: t.Kind, Routine: last.Name, Path: last.Path, Also: added[:len(added)-1]}
	if wantsJSON(cmd) {
		return writeJSON(cmd.OutOrStdout(), out)
	}
	w := cmd.OutOrStdout()
	if t.Description != "" {
		fmt.Fprintf(w, "%s: %s\n", t.ID, t.Description)
	}
	for _, x := range out.Also {
		fmt.Fprintf(w, "Installed the %s %s (%s), which it listens to, not trusted.\n", x.Kind, x.Name, x.Path)
	}
	switch t.Kind {
	case templates.KindProgram:
		fmt.Fprintf(w, "Installed %s as the program %q (%s), paused and not trusted.\nTurn it on (trust it and run it on its schedule): "+
			product.Command+" routines trust %s --resume\n", t.ID, name, last.Path, name)
	case templates.KindHook:
		fmt.Fprintf(w, "Installed %s as the hook %q (%s), off and not trusted.\nTurn it on (trust it and its watcher, and listen): "+
			product.Command+" hooks trust %s --resume\n", t.ID, name, last.Path, name)
	case templates.KindWatcher:
		fmt.Fprintf(w, "Installed %s as the watcher %q (%s), not trusted: it runs once trusted, while a hook that's on listens to it.\n"+
			"Trusting a hook that listens to it trusts it too ("+product.Command+" hooks trust <hook> --resume)\n", t.ID, name, last.Path)
	}
	return nil
}
