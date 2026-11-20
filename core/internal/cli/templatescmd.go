package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

// TemplateInfo is one row of `templates list --json`.
type TemplateInfo struct {
	templates.Template
	// RunsHere is false for templates for another OS (listed with --all).
	RunsHere bool `json:"runsHere"`
	// Installed is true when a routine with the template's name exists.
	Installed bool `json:"installed"`
}

// InstallOutput is `templates install --json`.
type InstallOutput struct {
	Template string `json:"template"`
	Routine  string `json:"routine"`
	Path     string `json:"path"`
}

// templatesDir is where the app's templates are: bundled next to the binary
// (product.TemplatesDir, relative to the executable), or the source checkout's
// in a development build.
func templatesDir() (string, error) {
	if dir := product.TemplatesDir; dir != "" {
		if filepath.IsAbs(dir) {
			return dir, nil
		}
		exe, err := os.Executable()
		if err != nil {
			return "", err
		}
		if exe, err = filepath.EvalSymlinks(exe); err != nil { // the CLI is a symlink into the app
			return "", err
		}
		return filepath.Join(filepath.Dir(exe), dir), nil
	}
	if product.SourceDir != "" {
		return filepath.Join(product.SourceDir, "templates"), nil
	}
	return "", errors.New("this build doesn't know where its templates are")
}

func newTemplatesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "templates",
		Short: "Routine templates shipped with the app, to install as your own routines (as is, or to adapt)",
	}
	cmd.AddCommand(newTemplatesListCmd(), newTemplatesInstallCmd())
	return cmd
}

// templateRows is the templates for this OS (all: every OS), each saying
// whether a routine of its name is installed.
func templateRows(a *engine.App, all bool) ([]TemplateInfo, error) {
	dir, err := templatesDir()
	if err != nil {
		return nil, err
	}
	list, err := templates.List(dir)
	if err != nil {
		return nil, err
	}
	set, err := routines.Load(a.Layout.Routines())
	if err != nil {
		return nil, err
	}
	rows := []TemplateInfo{}
	for _, t := range list {
		if !all && !t.RunsHere() {
			continue
		}
		_, findErr := set.Find(t.Name)
		rows = append(rows, TemplateInfo{Template: t, RunsHere: t.RunsHere(), Installed: findErr == nil})
	}
	return rows, nil
}

func newTemplatesListCmd() *cobra.Command {
	var all, asJSON bool
	cmd := &cobra.Command{
		Use:         "list",
		Annotations: readOnly(),
		Short:       "List templates for this OS (--all for every OS)",
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := loadApp(cmd)
			if err != nil {
				return err
			}
			rows, err := templateRows(a, all)
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), rows)
			}
			out := cmd.OutOrStdout()
			if len(rows) == 0 {
				fmt.Fprintf(out, "No templates for %s.\n", routines.Platform())
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "TEMPLATE\tPARAMETERS\tPLATFORMS\tINSTALLED\tDESCRIPTION")
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
				fmt.Fprintf(tw, "%s\t%s\t%s\t%v\t%s\n", r.ID, orDash(strings.Join(params, ", ")), platforms, r.Installed, oneLine(r.Description, 60))
			}
			_ = tw.Flush()
			fmt.Fprintln(out, "\n* required. Install with: "+product.Command+" templates install <template> [--as <name>] [--set param=value]")
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "include templates for other operating systems")
	jsonFlag(cmd.Flags(), &asJSON, []TemplateInfo{})
	return cmd
}

func newTemplatesInstallCmd() *cobra.Command {
	var as string
	var sets []string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "install <template>",
		Short: "Install a template as a routine; its parameter values become the routine's settings",
		Long: "Copies the template into the routines folder (never over an existing routine) and stores its " +
			"parameter values as routines.<name>.params.<param> settings. The routine starts paused and untrusted (turn " +
			"it on with `routines trust <name> --resume`). Values come from --set, then the " +
			"template's defaults; required ones without a value are asked for (or an error, without a terminal).",
		Example: "  " + product.Command + " templates install projects/acme/standup\n" +
			"  " + product.Command + " templates install personal/digest --set lookback=72h",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, unlock, err := loadAppForEdit(cmd)
			if err != nil {
				return err
			}
			defer unlock()
			dir, err := templatesDir()
			if err != nil {
				return err
			}
			t, err := templates.Find(dir, args[0])
			if err != nil {
				return err
			}
			if !t.RunsHere() {
				return fmt.Errorf("%s runs on %s, not %s", t.ID, strings.Join(t.Platforms, ", "), routines.Platform())
			}
			name := t.Name
			if as != "" {
				name = as
			}
			if !routines.ValidName(name) {
				return fmt.Errorf("%q can't name a routine: lowercase letters, digits and dashes", name)
			}
			values, err := paramValues(t, name, sets, cmd.InOrStdin(), cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			// Settings first (disabled, parameter values), the file last: the
			// scheduler never sees a half-installed or enabled routine.
			rdir := a.Layout.Routines()
			r := t.Routine
			r.Name = name
			if _, err = disableNew(a, rdir, r); err != nil {
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
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), InstallOutput{Template: t.ID, Routine: name, Path: path})
			}
			if t.Description != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", t.ID, t.Description)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Installed %s as %q (%s), paused and not trusted.\nTry it: "+product.Command+" run %s --dry-run\n"+
				"Turn it on (trust it and run it on its schedule): "+product.Command+" routines trust %s --resume\n",
				t.ID, name, path, name, name)
			return nil
		},
	}
	cmd.Flags().StringVar(&as, "as", "", "routine name (default: the template's name)")
	cmd.Flags().StringArrayVar(&sets, "set", nil, "a parameter value, param=value (repeatable)")
	jsonFlag(cmd.Flags(), &asJSON, InstallOutput{})
	return cmd
}

// paramValues validates --set values and asks for required parameters that
// have none, before anything is written.
func paramValues(t templates.Template, routine string, sets []string, in io.Reader, prompt io.Writer) (map[string]string, error) {
	byName := map[string]routines.Param{}
	for _, p := range t.Params {
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
	for _, p := range t.Params {
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
