package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
)

// RoutineDetail is `routines show --json`: a routine with its file's source.
type RoutineDetail struct {
	RoutineInfo
	// Source is the routine file as written (frontmatter and prompt).
	Source string `json:"source"`
}

// RoutineChange is the output of `routines create/update/delete --json`.
type RoutineChange struct {
	Routine string `json:"routine"`
	Path    string `json:"path"`
	// Enabled is whether the routine runs on its schedule after the change
	// (created routines start disabled; absent for deletes).
	Enabled *bool `json:"enabled,omitempty"`
}

// RoutineCheck is one row of `routines check --json`.
type RoutineCheck struct {
	Routine string `json:"routine"`
	OK      bool   `json:"ok"`
	// Problem is why the routine wouldn't run (file, settings, servers or tools).
	Problem string `json:"problem,omitempty"`
	// Tools are its allowlisted tools and the server offering each.
	Tools []agent.CheckedTool `json:"tools,omitempty"`
}

func newRoutinesShowCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:         "show <routine>",
		Short:       "Show a routine: its file, effective settings and last run",
		Annotations: readOnly(),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := loadApp(cmd)
			if err != nil {
				return err
			}
			set, err := routines.Load(a.Layout.Routines())
			if err != nil {
				return err
			}
			r, err := set.Find(args[0])
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
			d := RoutineDetail{RoutineInfo: routineInfo(cmd, a, def, status, r, time.Now()), Source: r.Source}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), d)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%s (%s)\n  enabled   %v\n", d.Name, d.Path, d.Enabled)
			if d.Schedule != "" {
				fmt.Fprintf(out, "  schedule  %s (staleness %s)\n", d.Schedule, d.Staleness)
			}
			fmt.Fprintf(out, "  timeout   %s\n  effort    %s\n", d.Timeout, d.Effort)
			if l := d.LastRun; l != nil {
				fmt.Fprintf(out, "  last run  %s %s\n", l.Outcome, l.StartedAt.Local().Format("Mon Jan 2 15:04"))
			}
			fmt.Fprintf(out, "\n%s", d.Source)
			return nil
		},
	}
	jsonFlag(cmd.Flags(), &asJSON, RoutineDetail{})
	return cmd
}

// sourceFlags are how create and update take a routine file's content.
type sourceFlags struct{ from, source string }

func (f *sourceFlags) add(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.from, "from", "", "read the routine file from this path (- for stdin)")
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
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "create <routine>",
		Short: "Add a routine file, validated first; it starts disabled",
		Long: "Writes <routine>.prompt to the routines folder after validating it like any routine (never over an " +
			"existing one). The routine starts paused and untrusted: try it with `run <routine> --dry-run`, then turn " +
			"it on with `routines trust <routine> --resume`. How to write the file: `" + product.Command + " guide routines`.",
		Example: "  " + product.Command + " routines create standup --from ./standup.prompt",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, unlock, err := loadAppForEdit(cmd)
			if err != nil {
				return err
			}
			defer unlock()
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
			undo, err := disableNew(a, dir, parsed)
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
		},
	}
	src.add(cmd)
	jsonFlag(cmd.Flags(), &asJSON, RoutineChange{})
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

// disableNew stores routines.<name>.enabled = false for a routine about to be
// written to dir, so it doesn't run on its schedule until it's enabled on
// purpose. It refuses a name already taken (whose setting isn't ours to
// change); undo removes the setting again if writing the routine fails.
func disableNew(a *engine.App, dir string, r routines.Routine) (undo func(), err error) {
	if _, err := os.Stat(filepath.Join(dir, r.Name+routines.Ext)); err == nil {
		return nil, fmt.Errorf("routine %q already exists", r.Name)
	}
	st := settings.Store{Path: a.Layout.Config(), Extra: r.Settings()}
	key := "routines." + r.Name + ".enabled"
	if _, err := st.Set(key, "false"); err != nil {
		return nil, err
	}
	return func() { _, _ = st.Reset(key) }, nil
}

func newRoutinesUpdateCmd() *cobra.Command {
	var src sourceFlags
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "update <routine>",
		Short: "Replace a routine's file, validated first (its settings are kept)",
		Long: "Replaces <routine>.prompt after validating it like any routine. Its settings are kept; changing what " +
			"it can reach takes its trust away. How to write the file: `" + product.Command + " guide routines`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, unlock, err := loadAppForEdit(cmd)
			if err != nil {
				return err
			}
			defer unlock()
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
		},
	}
	src.add(cmd)
	jsonFlag(cmd.Flags(), &asJSON, RoutineChange{})
	return cmd
}

func newRoutinesDeleteCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "delete <routine>",
		Short: "Delete a routine's file, its settings, trust and memories (run history is kept)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, unlock, err := loadAppForEdit(cmd)
			if err != nil {
				return err
			}
			defer unlock()
			path, err := routines.Delete(a.Layout.Routines(), args[0])
			if err != nil {
				return err
			}
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
		},
	}
	jsonFlag(cmd.Flags(), &asJSON, RoutineChange{})
	return cmd
}

func newRoutinesCheckCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "check [<routine>]",
		Short: "Check routines would run: file, settings, servers connect, allowlisted tools exist",
		Long: "Without calling the model or any tool: parses each routine file, resolves its settings (required " +
			"parameters set, folder exists), checks every server it uses is provided, connects to its servers and " +
			"confirms each allowlisted tool is offered by exactly one of them, as a run would, checks the servers " +
			"its plugins reach are signed in, and checks its model's provider is set up (a model on the local server: the server is asked for " +
			"its models). Fails if any routine has a problem.",
		Annotations: readOnly(),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := loadApp(cmd)
			if err != nil {
				return err
			}
			set, err := routines.Load(a.Layout.Routines())
			if err != nil {
				return err
			}
			var rows []RoutineCheck
			add := func(r routines.Routine) {
				row := RoutineCheck{Routine: r.Name}
				rd := a.Assess(r)
				rd.CheckTools(cmd.Context(), a, r)
				rd.CheckModel(cmd.Context(), a, r)
				switch {
				case rd.ConfigErr != nil:
					row.Problem = rd.ConfigErr.Error()
				case len(rd.Problems) > 0:
					row.Problem = rd.Problems[0].Text
				default:
					row.Tools = rd.Tools
				}
				row.OK = row.Problem == ""
				rows = append(rows, row)
			}
			if len(args) == 1 {
				r, err := set.Find(args[0])
				if err != nil {
					rows = append(rows, RoutineCheck{Routine: args[0], Problem: err.Error()})
				} else {
					add(r)
				}
			} else {
				for _, e := range set.Errors {
					rows = append(rows, RoutineCheck{Routine: e.Path, Problem: e.Err.Error()})
				}
				for _, r := range set.Routines {
					add(r)
				}
			}
			failed := 0
			for _, r := range rows {
				if !r.OK {
					failed++
				}
			}
			if asJSON {
				if err := writeJSON(cmd.OutOrStdout(), rows); err != nil {
					return err
				}
			} else {
				for _, r := range rows {
					if r.OK {
						fmt.Fprintf(cmd.OutOrStdout(), "ok    %s (%d tools)\n", r.Routine, len(r.Tools))
					} else {
						fmt.Fprintf(cmd.OutOrStdout(), "FAIL  %s: %s\n", r.Routine, r.Problem)
					}
				}
			}
			if failed > 0 {
				return reported(fmt.Errorf("%d of %d routines have problems", failed, len(rows)))
			}
			return nil
		},
	}
	jsonFlag(cmd.Flags(), &asJSON, []RoutineCheck{})
	return cmd
}
