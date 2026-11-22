package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/hooks"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
	"github.com/BorysSerbyn/process-automator/core/internal/secrets"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/watchers"
)

// Hooks are prompts or programs run when a watcher prints an event, not on
// a schedule: they have their own commands, and never show among routines.
// Adding or trusting one is mine, as for a program: neither is an MCP tool.

// HookInfo is one row of `hooks list --json`: a hook with its effective
// settings and last run.
type HookInfo struct {
	Name string `json:"name"`
	// Kind is program for a program hook; absent for an LLM hook (llm).
	Kind string `json:"kind,omitempty" jsonschema:"enum=llm,enum=program"`
	// Path is its file: its prompt, or its program's description file (in
	// the app's copy).
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
	// On is what it acts on: a watcher's events, of one type (or every type).
	On hooks.On `json:"on"`
	// Batch (an LLM hook's) gives one run every event that waited for it.
	Batch     bool   `json:"batch,omitempty"`
	Enabled   bool   `json:"enabled"`
	Staleness string `json:"staleness"`
	Timeout   string `json:"timeout"`
	// Rate is how many runs it may start in any hour.
	Rate int `json:"rate"`
	// Folder is where its runs work (absent: a temporary folder).
	Folder string `json:"folder,omitempty"`
	// Trusted is whether it may run for real (what it reaches and acts on
	// is what I trusted); Untrusted says why not.
	Trusted   bool   `json:"trusted"`
	Untrusted string `json:"untrusted,omitempty"`
	// Problem is why it can't run while it's on (its watcher missing,
	// untrusted or failing, ...); empty when it can.
	Problem string `json:"problem,omitempty"`
	// Servers, Tools, BuiltinTools and Model are an LLM hook's, as an LLM
	// routine's: BuiltinTools are the agent CLI built-in tools its runs may use.
	Servers      []string `json:"servers,omitempty"`
	Tools        []string `json:"tools,omitempty"`
	BuiltinTools []string `json:"builtinTools,omitempty"`
	Model        string   `json:"model,omitempty"`
	// Program is a program hook's own: what runs and with what.
	Program *ProgramInfo `json:"program,omitempty"`
	// LastRun is its latest outcome from run status.
	LastRun *runstatus.Run `json:"lastRun,omitempty"`
}

// HookDetail is `hooks show --json`: a hook with its file's source and its
// watcher.
type HookDetail struct {
	HookInfo
	// Source is its file as written.
	Source string `json:"source"`
	// Watcher is the watcher it listens to (absent when there's none by
	// that name).
	Watcher *WatcherInfo `json:"watcher,omitempty"`
}

// HookChange is the output of `hooks add/update/delete/pause/resume --json`.
type HookChange struct {
	Hook string `json:"hook"`
	// Kind is program for a program hook; absent for an LLM hook (llm).
	Kind string `json:"kind,omitempty" jsonschema:"enum=llm,enum=program"`
	Path string `json:"path"`
	// Enabled is whether it runs on its events after the change (added
	// hooks start off; absent for deletes).
	Enabled *bool `json:"enabled,omitempty"`
}

func newHooksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hooks",
		Short: "Hooks: prompts or programs run when a watcher prints an event, instead of on a schedule",
		Long: "A hook acts on a watcher's events (`" + product.Command + " watchers`): its action is a prompt (an LLM hook) or a " +
			"program. Hooks aren't routines: they have no schedule and never show among them. How to write one: `" +
			product.Command + " guide hooks`.",
	}
	cmd.AddCommand(newHooksListCmd(), newHooksShowCmd(), newHooksAddCmd(false), newHooksAddCmd(true), newHooksDeleteCmd(),
		newHooksTrustCmd(), newHooksPauseCmd(false), newHooksPauseCmd(true), newHooksFireCmd())
	return cmd
}

func newHooksListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "list",
		Short:       "List hooks: what each acts on, whether it's on, and its last run",
		Annotations: readOnly(),
		Args:        cobra.NoArgs,
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, _ []string) error {
			all, broken, err := a.Hooks()
			if err != nil {
				return err
			}
			warnBroken(cmd, broken)
			status, _ := runstatus.Load(a.Layout.RunStatus())
			rows := make([]HookInfo, 0, len(all))
			for _, x := range all {
				rows = append(rows, hookInfo(cmd, a, status, x))
			}
			if wantsJSON(cmd) {
				return writeJSON(cmd.OutOrStdout(), rows)
			}
			out := cmd.OutOrStdout()
			if len(rows) == 0 {
				fmt.Fprintf(out, "No hooks. Add one with `%s hooks add <name> --from <file>` (`%s guide hooks`).\n", product.Command, product.Command)
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tKIND\tENABLED\tACTS ON\tLAST RUN")
			for _, h := range rows {
				last := "-"
				if l := h.LastRun; l != nil {
					last = fmt.Sprintf("%s %s", l.Outcome, l.StartedAt.Local().Format("Mon Jan 2 15:04"))
				}
				k := h.Kind
				if k == "" {
					k = string(engine.KindLLM)
				}
				fmt.Fprintf(tw, "%s\t%s\t%v\t%s\t%s\n", h.Name, k, h.Enabled, h.On.String(), last)
			}
			return tw.Flush()
		}),
	}
	jsonFlag(cmd.Flags(), []HookInfo{})
	return cmd
}

// hookInfo is a hook with its effective settings and last run; problems
// with its settings are warned about.
func hookInfo(cmd *cobra.Command, a *engine.App, status *runstatus.Status, x engine.Runnable) HookInfo {
	h, _ := engine.AsHook(x)
	row := HookInfo{Name: x.Name(), Kind: x.Kind().Written(), Path: x.Path(), Description: x.Description(), On: h.On, Batch: h.Batch}
	c, err := a.HookConfig(x)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s: %v\n", x.Name(), err)
		row.Problem = err.Error()
	} else {
		row.Enabled, row.Rate, row.Folder = c.Enabled, c.Rate, c.WorkFolder
		row.Staleness, row.Timeout = settings.DurationText(c.Staleness), settings.DurationText(c.Timeout)
		rd := a.Assess(x)
		var why []string
		for _, p := range rd.Problems {
			why = append(why, p.Text)
		}
		row.Problem = strings.Join(why, "; ")
		if rd.TrustErr == nil {
			row.Trusted, row.Untrusted = rd.Untrusted == "", rd.Untrusted
		}
	}
	if r := h.Prompt; r != nil {
		row.Servers, row.Tools, row.Model = r.Servers, r.Tools, r.Model
		if rc, err := a.RoutineConfig(*r); err == nil {
			row.BuiltinTools = rc.BuiltinTools
		}
	}
	if p := h.Program; p != nil {
		row.Program = programInfo(a, *p)
	}
	row.LastRun = status.LastRun(x.Name())
	return row
}

func newHooksShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "show <hook>",
		Short:       "Show a hook: its file, what it acts on, its watcher, its settings and last run",
		Annotations: readOnly(),
		Args:        cobra.ExactArgs(1),
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, args []string) error {
			x, err := a.FindHook(args[0])
			if err != nil {
				return err
			}
			status, _ := runstatus.Load(a.Layout.RunStatus())
			d := HookDetail{HookInfo: hookInfo(cmd, a, status, x), Source: x.Source()}
			if w, err := a.FindWatcher(d.On.Watcher); err == nil {
				info := watcherInfo(a, w, nil)
				d.Watcher = &info
			}
			if wantsJSON(cmd) {
				return writeJSON(cmd.OutOrStdout(), d)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%s (%s)\n  acts on   %s\n  enabled   %v\n  trusted   %v\n", d.Name, d.Path, d.On.String(), d.Enabled, d.Trusted)
			if d.Untrusted != "" {
				fmt.Fprintf(out, "            %s\n", d.Untrusted)
			}
			fmt.Fprintf(out, "  staleness %s\n  timeout   %s\n  rate      %d an hour\n", d.Staleness, d.Timeout, d.Rate)
			if d.Problem != "" {
				fmt.Fprintf(out, "  problem   %s\n", d.Problem)
			}
			if l := d.LastRun; l != nil {
				fmt.Fprintf(out, "  last run  %s %s\n", l.Outcome, l.StartedAt.Local().Format("Mon Jan 2 15:04"))
			}
			fmt.Fprintf(out, "\n%s", d.Source)
			return nil
		}),
	}
	jsonFlag(cmd.Flags(), HookDetail{})
	return cmd
}

func newHooksAddCmd(update bool) *cobra.Command {
	var from string
	use, short := "add <hook>", "Add a hook from its file (a .prompt file, or a hook.yaml beside its executable); it starts off and untrusted"
	if update {
		use, short = "update <hook>", "Replace a hook's file (its settings are kept); a changed action or trigger takes its trust away"
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long: "--from is the hook's file: a Dotprompt file (an LLM hook), or a hook.yaml naming the executable beside it " +
			"(a program hook), which is copied with it. A hook is added off and untrusted: review and turn it on with `" +
			product.Command + " hooks trust <hook> --resume` (its watcher needs trusting too). Only you add a hook, from a " +
			"terminal. How to write one: `" + product.Command + " guide hooks`.",
		Example: "  " + product.Command + " hooks add open-meet --from ./open-meet/hook.yaml\n" +
			"  " + product.Command + " hooks add triage --from ./triage.prompt",
		Args: cobra.ExactArgs(1),
		RunE: withAppForEdit(func(cmd *cobra.Command, a *engine.App, args []string) error {
			name := args[0]
			if from == "" || from == "-" {
				return errors.New("a hook is added from its file's path (--from <path>), in a terminal")
			}
			raw, err := os.ReadFile(from)
			if err != nil {
				return err
			}
			parsed, err := hooks.Parse(name, from, string(raw))
			if err != nil {
				return err
			}
			if parsed.Prompt != nil {
				// As routines create does: no servers or tools that don't exist.
				if err := checkRoutineTools(cmd, a, *parsed.Prompt); err != nil {
					return err
				}
			}
			if parsed.Program != nil && len(parsed.Program.Secrets) > 0 {
				if s, ok := a.FindSignIn(name); ok && s.Kind != "hook" {
					return fmt.Errorf("%s is already a sign-in's name (`%s auth login %s`): give a hook with secrets another name", name, product.Command, name)
				}
			}
			off := false
			out := HookChange{Hook: name}
			if !update {
				if why := a.NameTaken(name); why != "" {
					return errors.New(why)
				}
				// Off before it exists, so the daemon never sees it on.
				st := settings.Store{Path: a.Layout.Config(), Extra: parsed.Settings()}
				if _, err := st.Set("hooks."+name+".enabled", "false"); err != nil {
					return err
				}
				out.Enabled = &off
			}
			h, err := hooks.Add(a.Layout.Hooks(), name, from, update)
			if err != nil {
				if !update {
					_, _ = (settings.Store{Path: a.Layout.Config(), Extra: parsed.Settings()}).Reset("hooks." + name + ".enabled")
				}
				return err
			}
			out.Path = h.Path
			if h.Program != nil {
				out.Kind = engine.KindProgram.Written()
			}
			if !update {
				return emit(cmd, out, fmt.Sprintf("Added the hook %s (%s), off and not trusted. It acts on %s.\n"+
					"Turn it on (trust it, and listen): %s hooks trust %s --resume", name, h.Path, h.On.String(), product.Command, name))
			}
			text := fmt.Sprintf("Updated the hook %s (%s).", name, h.Path)
			if x, err := a.FindHook(name); err == nil {
				if why, err := a.Untrusted(x); err == nil && why != "" {
					text += " It isn't trusted: " + why
				}
			}
			return emit(cmd, out, text)
		}),
	}
	cmd.Flags().StringVar(&from, "from", "", "the hook's file: a .prompt file, or a hook.yaml with its executable beside it")
	hideFromMCP(cmd, "from")
	jsonFlag(cmd.Flags(), HookChange{})
	return cmd
}

func newHooksDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <hook>",
		Short: "Delete a hook: its copy, settings, trust and secrets (its run history is kept)",
		Args:  cobra.ExactArgs(1),
		RunE: withAppForEdit(func(cmd *cobra.Command, a *engine.App, args []string) error {
			name := args[0]
			var keys []string
			if s, ok := a.FindSignIn(name); ok && s.Kind == "hook" {
				for _, sec := range s.Secrets {
					keys = append(keys, sec.Key)
				}
			}
			path, err := hooks.Delete(a.Layout.Hooks(), name)
			if err != nil {
				return err
			}
			if err := (settings.Store{Path: a.Layout.Config()}).ResetPrefix("hooks." + name + "."); err != nil {
				return fmt.Errorf("deleted %s, but its settings remain: %w", path, err)
			}
			if err := a.TrustStore().Forget(name); err != nil {
				return fmt.Errorf("deleted %s, but not its trust: %w", path, err)
			}
			for _, k := range keys {
				if err := a.Connector.Secrets.Delete(k); err != nil && !errors.Is(err, secrets.ErrNotFound) {
					return fmt.Errorf("deleted %s, but not its secrets: %w", path, err)
				}
			}
			return emit(cmd, HookChange{Hook: name, Path: path}, fmt.Sprintf("Deleted the hook %s and its settings.", name))
		}),
	}
	jsonFlag(cmd.Flags(), HookChange{})
	return cmd
}

func newHooksTrustCmd() *cobra.Command {
	var yes, check, resume bool
	cmd := &cobra.Command{
		Use:   "trust <hook>",
		Short: "Review what a hook acts on and what it can reach, and trust it (--resume: and turn it on)",
		Long: "Shows what the hook acts on (its watcher and event type), whether its watcher is trusted (it needs its own " +
			"trust: `" + product.Command + " watchers trust`), and its action: an LLM hook's servers and tools, or a program " +
			"hook's executable. An LLM hook that can write is flagged: it acts on outside data. Another watcher, event type or " +
			"action takes the trust away until you trust it again. With --resume it also turns the hook on, in the same yes.",
		Example: "  " + product.Command + " hooks trust open-meet --check --json   # review only\n" +
			"  " + product.Command + " hooks trust open-meet --yes --resume    # trust it and turn it on",
		Args: cobra.ExactArgs(1),
		RunE: withAppForEdit(func(cmd *cobra.Command, a *engine.App, args []string) error {
			x, err := a.FindHook(args[0])
			if err != nil {
				return err
			}
			return trustRunnable(cmd, a, x, yes, check, resume)
		}),
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "don't ask (after reviewing it)")
	cmd.Flags().BoolVar(&check, "check", false, "only show what it acts on and can reach, and what changed; trust nothing")
	cmd.Flags().BoolVar(&resume, "resume", false, "also turn it on, if it's off")
	cmd.MarkFlagsMutuallyExclusive("check", "yes")
	cmd.MarkFlagsMutuallyExclusive("check", "resume")
	jsonFlag(cmd.Flags(), TrustOutput{})
	return cmd
}

func newHooksPauseCmd(resume bool) *cobra.Command {
	use, short, verb := "pause <hook>", "Turn a hook off: it no longer runs on its events", "Paused"
	if resume {
		use, short, verb = "resume <hook>", "Turn a paused hook on again", "Resumed"
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, args []string) error {
			x, err := a.FindHook(args[0])
			if err != nil {
				return err
			}
			st, err := settingsStore(cmd)
			if err != nil {
				return err
			}
			if _, err := st.Set(enabledKey(x), fmt.Sprint(resume)); err != nil {
				return err
			}
			return emit(cmd, HookChange{Hook: x.Name(), Kind: x.Kind().Written(), Path: x.Path(), Enabled: &resume},
				fmt.Sprintf("%s %s.", verb, x.Name()))
		}),
	}
	jsonFlag(cmd.Flags(), HookChange{})
	return cmd
}

func newHooksFireCmd() *cobra.Command {
	var event, eventJSON string
	var dryRun bool
	cmd := &cobra.Command{
		Use: "fire <hook>",
		Annotations: map[string]string{
			annReadOnlyWith: "dry-run",
			annNotReadOnly:  "firing a hook runs it, even a dry run (an LLM hook uses the model)",
		},
		Short: "Run a hook now for a sample event (a file or stdin), as its watcher would: the way to try one out",
		Long: "A hook has no run now: it runs for an event. fire gives it one, as a line its watcher would print (id, type, " +
			"and optionally at and data), whatever its staleness and rate, and runs it as `run` runs a routine: --dry-run " +
			"intercepts an LLM hook's writes, and passes --dry-run to a program hook that writes. A real run needs trust; a " +
			"program hook needs it for a dry run too. Its run is recorded with trigger manual and the event.",
		Example: "  " + product.Command + " hooks fire open-meet --event sample.json --dry-run\n" +
			"  echo '{\"id\":\"1\",\"type\":\"meeting.started\",\"data\":{\"link\":\"https://meet.google.com/abc\"}}' | " +
			product.Command + " hooks fire open-meet --event -",
		Args: cobra.ExactArgs(1),
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, args []string) error {
			x, err := a.FindHook(args[0])
			if err != nil {
				return err
			}
			var raw []byte
			switch {
			case (event == "") == (eventJSON == ""):
				return errors.New("give the sample event: --event <file> (- for stdin)")
			case eventJSON != "":
				raw = []byte(eventJSON)
			case event == "-":
				if raw, err = io.ReadAll(cmd.InOrStdin()); err != nil {
					return err
				}
			default:
				if raw, err = os.ReadFile(event); err != nil {
					return err
				}
			}
			ev, err := watchers.ParseEvent(raw, time.Now())
			if err != nil {
				return err
			}
			res, err := a.Fire(cmd.Context(), x, ev, engine.RunOptions{DryRun: dryRun, Log: cmd.ErrOrStderr()})
			if err != nil {
				return err
			}
			out := RunOutput{Result: res.Agent, Kind: res.Kind.Written(), Program: res.Program}
			if wantsJSON(cmd) {
				return writeJSON(cmd.OutOrStdout(), out)
			}
			printResult(cmd, out)
			return nil
		}),
	}
	cmd.Flags().StringVar(&event, "event", "", "the sample event's file: a JSON object as its watcher prints it (- for stdin)")
	cmd.Flags().StringVar(&eventJSON, "event-json", "", "the sample event itself, as JSON")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "intercept an LLM hook's side-effecting tool calls; a program hook that writes gets --dry-run")
	// Over MCP the event comes as its JSON (on stdin), never as a path to read.
	hideFromMCP(cmd, "event")
	_ = cmd.Flags().SetAnnotation("event-json", annViaStdin, []string{"event"})
	jsonFlag(cmd.Flags(), RunOutput{})
	return cmd
}
