package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/programs"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
)

// RoutineDetail is `routines show --json`: a routine of either kind with its
// file's source.
type RoutineDetail struct {
	RoutineInfo
	// Source is its file as written: an LLM routine's frontmatter and
	// prompt, a program's description file.
	Source string `json:"source"`
}

// RoutineChange is the output of `routines create/update/delete --json`.
type RoutineChange struct {
	Routine string `json:"routine"`
	// Kind is program for a program; absent for an LLM routine (llm).
	Kind string `json:"kind,omitempty" jsonschema:"enum=llm,enum=program"`
	Path string `json:"path"`
	// Enabled is whether the routine runs on its schedule after the change
	// (created routines start disabled; absent for deletes).
	Enabled *bool `json:"enabled,omitempty"`
	// Hook is true when the change is to a hook (paused or resumed by name).
	Hook bool `json:"hook,omitempty"`
}

// RoutineCheck is one row of `routines check --json`.
type RoutineCheck struct {
	Routine string `json:"routine"`
	// Kind is program for a program; absent for an LLM routine (llm).
	Kind string `json:"kind,omitempty" jsonschema:"enum=llm,enum=program"`
	OK   bool   `json:"ok"`
	// Problem is why the routine wouldn't run (file, settings, servers or tools).
	Problem string `json:"problem,omitempty"`
	// Tools are an LLM routine's allowlisted tools and the server offering each.
	Tools []agent.CheckedTool `json:"tools,omitempty"`
	// Warnings don't keep it from running, but want fixing (an LLM routine
	// without an outcome contract).
	Warnings []string `json:"warnings,omitempty"`
}

func newRoutinesShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "show <routine>",
		Short:       "Show a routine of either kind: its file, effective settings and last run",
		Annotations: readOnly(),
		Args:        cobra.ExactArgs(1),
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, args []string) error {
			x, err := a.Find(args[0])
			if err != nil {
				return err
			}
			def, err := a.BackendDef()
			if err != nil {
				return err
			}
			status, err := runstatus.Load(a.Layout.RunStatus())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: can't read run status: %v\n", err)
			}
			d := RoutineDetail{RoutineInfo: routineInfo(cmd, a, def, status, x, time.Now()), Source: x.Source()}
			if wantsJSON(cmd) {
				return writeJSON(cmd.OutOrStdout(), d)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%s (%s)\n  enabled   %v\n", d.Name, d.Path, d.Enabled)
			if d.Schedule != "" {
				fmt.Fprintf(out, "  schedule  %s (staleness %s)\n", d.Schedule, d.Staleness)
			}
			fmt.Fprintf(out, "  timeout   %s\n", d.Timeout)
			if d.Effort != "" {
				fmt.Fprintf(out, "  effort    %s\n", d.Effort)
			}
			if p := d.Program; p != nil {
				fmt.Fprintf(out, "  program   %s (writes: %v)\n", p.Executable, p.Writes)
			}
			if l := d.LastRun; l != nil {
				fmt.Fprintf(out, "  last run  %s %s\n", l.Outcome, l.StartedAt.Local().Format("Mon Jan 2 15:04"))
			}
			fmt.Fprintf(out, "\n%s", d.Source)
			return nil
		}),
	}
	jsonFlag(cmd.Flags(), RoutineDetail{})
	return cmd
}

// sourceFlags are how create and update take a routine file's content.
type sourceFlags struct{ from, source string }

func (f *sourceFlags) add(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.from, "from", "", "read the routine file from this path (- for stdin); a program's description file, from its path")
	cmd.Flags().StringVar(&f.source, "source", "", "the routine file's content (frontmatter and prompt)")
	// Over MCP the content comes as --source; reading paths would let a client
	// copy any file it names into a routine and read it back.
	hideFromMCP(cmd, "from")
	_ = cmd.Flags().SetAnnotation("source", annViaStdin, []string{"from"})
}

func (f *sourceFlags) read(in io.Reader) (string, error) {
	switch {
	case (f.from == "") == (f.source == ""):
		return "", errors.New("give the routine's content: source (the file's text), or --from <path> on a terminal")
	case f.source != "":
		return f.source, nil
	case f.from == "-":
		b, err := io.ReadAll(in)
		return string(b), err
	default:
		b, err := os.ReadFile(f.from)
		return string(b), err
	}
}

func newRoutinesCreateCmd() *cobra.Command {
	var src sourceFlags
	var kind string
	cmd := &cobra.Command{
		Use:   "create <routine>",
		Short: "Add a routine (an LLM routine; --kind program: a program), validated first; it starts disabled",
		Long: "Writes <routine>.prompt to the routines folder after validating it like any routine (never over an " +
			"existing one, of either kind). The routine starts paused and untrusted: try it with `run <routine> --dry-run`, then turn " +
			"it on with `routines trust <routine> --resume`. How to write the file: `" + product.Command + " guide routines`.\n\n" +
			"With --kind program it adds a program instead: --from is its description file (program.yaml), and the " +
			"executable it names is copied with it into the app's programs folder; that copy is what runs. It starts " +
			"paused and untrusted too, and runs only once trusted, dry runs included. How to write one: `" + product.Command + " guide programs`.",
		Example: "  " + product.Command + " routines create standup --from ./standup.prompt\n" +
			"  " + product.Command + " routines create backup --kind program --from ./backup/program.yaml",
		Args: cobra.ExactArgs(1),
		RunE: withAppForEdit(func(cmd *cobra.Command, a *engine.App, args []string) error {
			k, err := engine.ParseKind(kind)
			if err != nil {
				return err
			}
			if k == engine.KindProgram {
				return addProgram(cmd, a, args[0], src, false)
			}
			source, err := src.read(cmd.InOrStdin())
			if err != nil {
				return err
			}
			dir := a.Layout.Routines()
			parsed, err := routines.Parse(args[0], source)
			if err != nil {
				return err
			}
			if err := checkRoutineTools(cmd, a, parsed); err != nil {
				return err
			}
			// Disabled before the file exists, so the scheduler never sees it enabled.
			undo, err := disableNew(a, parsed.Name, parsed.Settings())
			if err != nil {
				return err
			}
			r, err := routines.Create(dir, args[0], source)
			if err != nil {
				undo()
				return err
			}
			off := false
			return emit(cmd, RoutineChange{Routine: r.Name, Path: r.Path, Enabled: &off},
				fmt.Sprintf("Created %s (%s), paused and not trusted.\nTry it: "+product.Command+" run %s --dry-run\n"+
					"Turn it on (trust it and run it on its schedule): "+product.Command+" routines trust %s --resume",
					r.Name, r.Path, r.Name, r.Name))
		}),
	}
	src.add(cmd)
	cmd.Flags().StringVar(&kind, "kind", "", "llm (the default), or program")
	// Adding a program is mine: over MCP, only LLM routines are created.
	hideFromMCP(cmd, "kind")
	jsonFlag(cmd.Flags(), RoutineChange{})
	return cmd
}

// toolsCheckTimeout bounds asking a routine's servers for their tools when
// it's written.
const toolsCheckTimeout = 30 * time.Second

// checkRoutineTools refuses a routine whose servers or tools don't exist, as
// a run would find them: a server no installed plugin or server provides, or
// a tool none of its servers offers (or two do). Its servers are asked, as
// `routines check` does; one that can't be asked now (not signed in yet,
// unreachable) leaves its tools unchecked, with a warning, so a routine can be
// written before signing in.
func checkRoutineTools(cmd *cobra.Command, a *engine.App, r routines.Routine) error {
	for _, s := range a.Servers(r) {
		if !a.KnownServer(s) {
			return fmt.Errorf("%s uses %s, which no installed plugin or server provides (`%s plugins list`)", r.Name, s, product.Command)
		}
	}
	if len(r.Tools) == 0 {
		return nil
	}
	folder := ""
	if c, err := a.RoutineConfig(r); err == nil {
		folder = c.WorkFolder
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), toolsCheckTimeout)
	defer cancel()
	_, err := agent.CheckTools(ctx, a.Connector, engine.RunOf(r, folder, ""), r.Servers, r.Tools)
	var wrong *agent.ToolsError
	switch {
	case errors.As(err, &wrong):
		return fmt.Errorf("%s: %w", r.Name, err)
	case err != nil:
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s's tools weren't checked (%v); `%s routines check %s` checks them once that's fixed\n",
			r.Name, err, product.Command, r.Name)
	}
	return nil
}

// disableNew stores routines.<name>.enabled = false for a routine of either
// kind about to be written (defs are its settings), so it doesn't run on its
// schedule until it's enabled on purpose. It refuses a name already taken by
// either kind (whose setting isn't ours to change); undo removes the setting
// again if writing the routine fails.
func disableNew(a *engine.App, name string, defs []settings.Setting) (undo func(), err error) {
	return a.DisableNew(name, defs)
}

func newRoutinesUpdateCmd() *cobra.Command {
	var src sourceFlags
	cmd := &cobra.Command{
		Use:   "update <routine>",
		Short: "Replace a routine's file, validated first (its settings are kept); a program's from its description file",
		Long: "Replaces <routine>.prompt after validating it like any routine. Its settings are kept; changing what " +
			"it can reach takes its trust away. How to write the file: `" + product.Command + " guide routines`.\n\n" +
			"For a program, --from is its description file's path: it and the executable it names are copied again; " +
			"a changed executable or description file takes its trust away. How to write one: `" + product.Command + " guide programs`.",
		Args: cobra.ExactArgs(1),
		RunE: withAppForEdit(func(cmd *cobra.Command, a *engine.App, args []string) error {
			if programs.Exists(a.Layout.Programs(), args[0]) {
				return addProgram(cmd, a, args[0], src, true)
			}
			source, err := src.read(cmd.InOrStdin())
			if err != nil {
				return err
			}
			parsed, err := routines.Parse(args[0], source)
			if err != nil {
				return err
			}
			if err := checkRoutineTools(cmd, a, parsed); err != nil {
				return err
			}
			r, err := routines.Update(a.Layout.Routines(), args[0], source)
			if err != nil {
				return err
			}
			out := RoutineChange{Routine: r.Name, Path: r.Path}
			text := fmt.Sprintf("Updated %s (%s).", r.Name, r.Path)
			if c, err := a.RoutineConfig(r); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s: %v\n", r.Name, err)
			} else {
				out.Enabled = &c.Enabled
				text = fmt.Sprintf("Updated %s (%s); enabled: %v.", r.Name, r.Path, c.Enabled)
			}
			return emit(cmd, out, text)
		}),
	}
	src.add(cmd)
	jsonFlag(cmd.Flags(), RoutineChange{})
	return cmd
}

func newRoutinesDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <routine>",
		Short: "Delete a routine of either kind: its file, settings, trust and memories (run history is kept)",
		Long: "Deletes the routine's file, its settings, trust and memories; its run history is kept. A program's " +
			"copy is deleted with its settings, trust and secrets; its run history and data folder are kept.",
		Args: cobra.ExactArgs(1),
		RunE: withAppForEdit(func(cmd *cobra.Command, a *engine.App, args []string) error {
			if programs.Exists(a.Layout.Programs(), args[0]) {
				return deleteProgram(cmd, a, args[0])
			}
			path, err := routines.Delete(a.Layout.Routines(), args[0])
			if err != nil {
				return err
			}
			_ = a.ForgetInstall(args[0]) // a record left behind only makes a later one of its name look installed from it
			if err := (settings.Store{Path: a.Layout.Config()}).ResetPrefix("routines." + args[0] + "."); err != nil {
				return fmt.Errorf("deleted %s, but its settings remain: %w", path, err)
			}
			if err := a.TrustStore().Forget(args[0]); err != nil {
				return fmt.Errorf("deleted %s, but not its trust: %w", path, err)
			}
			if err := a.Memories().Forget(args[0]); err != nil {
				return fmt.Errorf("deleted %s, but not its memories: %w", path, err)
			}
			return emit(cmd, RoutineChange{Routine: args[0], Path: path},
				fmt.Sprintf("Deleted %s (%s) and its settings.", args[0], path))
		}),
	}
	jsonFlag(cmd.Flags(), RoutineChange{})
	return cmd
}

func newRoutinesCheckCmd() *cobra.Command {
	var kind string
	cmd := &cobra.Command{
		Use:   "check [<routine>]",
		Short: "Check routines would run: file, settings, servers connect, allowlisted tools exist",
		Long: "Without calling the model or any tool: parses each routine file, resolves its settings (required " +
			"parameters set, folder exists), checks every server it uses is provided, connects to its servers and " +
			"confirms each allowlisted tool is offered by exactly one of them, as a run would, checks the servers " +
			"its plugins reach are signed in, and checks its model's provider is set up (a model on the local server: the server is asked for " +
			"its models). A program: its description file and settings, and its secrets stored. Without a routine, " +
			"it checks the LLM routines (--kind program or all: programs too). Fails if any routine has a problem.",
		Annotations: readOnly(),
		Args:        cobra.MaximumNArgs(1),
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, args []string) error {
			var rows []RoutineCheck
			add := func(x engine.Runnable) {
				row := RoutineCheck{Routine: x.Name(), Kind: x.Kind().Written()}
				rd := a.Assess(x)
				rd.Check(cmd.Context(), a, x)
				if rd.ConfigErr != nil || len(rd.Problems) > 0 {
					row.Problem = rd.All()[0].Text
				} else {
					row.Tools = rd.Tools
				}
				row.OK = row.Problem == ""
				row.Warnings = rd.Warnings
				rows = append(rows, row)
			}
			if len(args) == 1 {
				x, err := a.Find(args[0])
				if err != nil {
					rows = append(rows, RoutineCheck{Routine: args[0], Problem: err.Error()})
				} else {
					add(x)
				}
			} else {
				kinds, err := listedKinds(kind)
				if err != nil {
					return err
				}
				all, broken, err := a.All(kinds...)
				if err != nil {
					return err
				}
				for _, b := range broken {
					rows = append(rows, RoutineCheck{Routine: b.Path, Kind: b.Kind.Written(), Problem: b.Err.Error()})
				}
				for _, x := range all {
					add(x)
				}
			}
			return reportChecks(cmd, rows, "routines", func(r RoutineCheck) (bool, string) {
				var warn string
				for _, w := range r.Warnings {
					warn += "\n      warning: " + w
				}
				if r.OK {
					if r.Kind != "" {
						return true, fmt.Sprintf("ok    %s (%s)%s", r.Routine, r.Kind, warn)
					}
					return true, fmt.Sprintf("ok    %s (%d tools)%s", r.Routine, len(r.Tools), warn)
				}
				return false, fmt.Sprintf("FAIL  %s: %s%s", r.Routine, r.Problem, warn)
			})
		}),
	}
	cmd.Flags().StringVar(&kind, "kind", "", "without a routine: llm (the default), program, or all")
	jsonFlag(cmd.Flags(), []RoutineCheck{})
	return cmd
}
