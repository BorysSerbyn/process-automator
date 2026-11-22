package cli

import (
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/programs"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
	"github.com/BorysSerbyn/process-automator/core/internal/schedule"
	"github.com/BorysSerbyn/process-automator/core/internal/scheduler"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
)

// RoutineInfo is one row of `routines list --json`: a routine of either kind
// with its effective settings (file values overridden by settings). A
// program's row has kind program and its program section; what only an LLM
// routine has is empty for it (no servers, tools or built-in tools).
type RoutineInfo struct {
	Name string `json:"name"`
	// Kind is program for a program; absent for an LLM routine (llm).
	Kind string `json:"kind,omitempty" jsonschema:"enum=llm,enum=program"`
	// Path is its file: an LLM routine's .prompt file, a program's
	// description file (in the app's copy).
	Path string `json:"path"`
	// Description is what its file says it does.
	Description string   `json:"description,omitempty"`
	Servers     []string `json:"servers"`
	Tools       []string `json:"tools"`
	// Model is the routine's model as written in its file (empty: the backend's default).
	Model   string `json:"model,omitempty"`
	Enabled bool   `json:"enabled"`
	// Schedule is the cron schedule; absent for routines run only by hand.
	Schedule string `json:"schedule,omitempty"`
	// ScheduleText is the schedule in plain words ("At 09:20, Monday through Friday").
	ScheduleText string `json:"scheduleText,omitempty"`
	Staleness    string `json:"staleness,omitempty"`
	Timeout      string `json:"timeout"`
	// Effort is the routine's effort setting ("default" leaves it to the
	// backend); empty for a program.
	Effort string `json:"effort"`
	// Folder is where its runs work (absent: a temporary folder; a
	// program's data folder); frontends can filter routines by it.
	Folder string `json:"folder,omitempty"`
	// Session is "save" when runs keep their agent CLI session, else
	// "discard"; empty for a program.
	Session string `json:"session"`
	// BuiltinTools are the agent CLI built-in tools its runs may use.
	BuiltinTools []string `json:"builtinTools"`
	// Trusted is whether it may run for real (what it can reach is what I
	// trusted); Untrusted says why not.
	Trusted   bool   `json:"trusted"`
	Untrusted string `json:"untrusted,omitempty"`
	// TurnOn is true while turning it on (`routines trust <name> --yes
	// --resume`) has something to do: it isn't trusted, or it's paused with
	// a schedule. Frontends offer Turn On… while it is.
	TurnOn bool `json:"turnOn,omitempty"`
	// AgentModel and AgentEffort are the model and effort as the configured
	// backend's agent CLI takes them (claude --model/--effort; codex --model and
	// model_reasoning_effort), worked out by the core so frontends never
	// translate; empty means the CLI's default.
	AgentModel  string `json:"agentModel,omitempty"`
	AgentEffort string `json:"agentEffort,omitempty"`
	// Next is the next scheduled run (absent when disabled or manual-only).
	Next *time.Time `json:"next,omitempty"`
	// LastRun is the latest outcome from run status.
	LastRun *runstatus.Run `json:"lastRun,omitempty"`
	// Program is a program's own: what runs and with what.
	Program *ProgramInfo `json:"program,omitempty"`
}

// ProgramInfo is a program's section of its routine row: the copy of its
// executable the app runs, and what its description file declares.
type ProgramInfo struct {
	// Executable is the app's copy, the one that runs.
	Executable string `json:"executable"`
	// Checksum is the executable's SHA-256 (trust is keyed to it).
	Checksum string   `json:"checksum,omitempty"`
	Args     []string `json:"args"`
	// Writes is whether it can change anything: a dry run passes it
	// --dry-run; one that doesn't write runs as it is.
	Writes  bool            `json:"writes"`
	Secrets []ProgramSecret `json:"secrets"`
}

// ProgramSecret is a secret a program declares, and whether it's stored
// (`auth login <program>` stores it).
type ProgramSecret struct {
	Name string `json:"name"`
	// Env is the environment variable it's passed as.
	Env    string `json:"env"`
	Stored bool   `json:"stored"`
}

func newRoutinesCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "routines", Short: "Routines installed on this device: LLM routines (a .prompt file each) and, with --kind, programs"}
	var kind string
	list := &cobra.Command{
		Use:         "list",
		Annotations: readOnly(),
		Short:       "List routines with their schedule and enabled state (LLM routines; --kind program or all for programs)",
		Args:        cobra.NoArgs,
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, _ []string) error {
			kinds, err := listedKinds(kind)
			if err != nil {
				return err
			}
			all, broken, err := a.All(kinds...)
			if err != nil {
				return err
			}
			warnBroken(cmd, broken)
			status, err := runstatus.Load(a.Layout.RunStatus())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: can't read run status: %v\n", err)
			}
			now := time.Now()
			def, err := a.BackendDef()
			if err != nil {
				return err
			}
			rows := make([]RoutineInfo, 0, len(all))
			for _, x := range all {
				rows = append(rows, routineInfo(cmd, a, def, status, x, now))
			}
			if wantsJSON(cmd) {
				return writeJSON(cmd.OutOrStdout(), rows)
			}
			out := cmd.OutOrStdout()
			dir := a.Layout.Routines()
			if len(rows) == 0 {
				fmt.Fprintf(out, "No routines. Add <name>.prompt files to %s (shared snippets: _<name>.prompt)\n", dir)
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tKIND\tENABLED\tSCHEDULE\tNEXT\tLAST RUN")
			for _, r := range rows {
				sched, next, last := r.Schedule, "-", "-"
				if sched == "" {
					sched = "(manual)"
				}
				if r.Next != nil {
					next = r.Next.Format("Mon Jan 2 15:04")
				}
				if l := r.LastRun; l != nil {
					last = fmt.Sprintf("%s %s", l.Outcome, l.StartedAt.Local().Format("Mon Jan 2 15:04"))
				}
				k := r.Kind
				if k == "" {
					k = string(engine.KindLLM)
				}
				fmt.Fprintf(tw, "%s\t%s\t%v\t%s\t%s\t%s\n", r.Name, k, r.Enabled, sched, next, last)
			}
			_ = tw.Flush()
			fmt.Fprintf(out, "\nFolder: %s\n", dir)
			return nil
		}),
	}
	list.Flags().StringVar(&kind, "kind", "", "llm (the default), program, or all")
	jsonFlag(list.Flags(), []RoutineInfo{})
	cmd.AddCommand(list, newRoutinesShowCmd(), newRoutinesCreateCmd(), newRoutinesUpdateCmd(), newRoutinesDeleteCmd(), newRoutinesCheckCmd(), newRoutinesTrustCmd(), newRoutinesPauseCmd(false), newRoutinesPauseCmd(true))
	return cmd
}

// listedKinds are the kinds --kind names, where none is given (listing,
// creating) an LLM routine: llm (the default), program, or all.
func listedKinds(kind string) ([]engine.Kind, error) {
	if kind == "all" {
		return engine.Kinds, nil
	}
	k, err := engine.ParseKind(kind)
	if err != nil {
		return nil, fmt.Errorf("--kind %q: llm, program or all", kind)
	}
	return []engine.Kind{k}, nil
}

// routineInfo is a routine of either kind with its effective settings and
// last run; problems with its settings are warned about, not fatal.
func routineInfo(cmd *cobra.Command, a *engine.App, def engine.BackendDef, status *runstatus.Status, x engine.Runnable, now time.Time) RoutineInfo {
	rd := a.Assess(x)
	c := rd.Config
	if rd.ConfigErr != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s: %v\n", x.Name(), rd.ConfigErr)
	}
	row := RoutineInfo{
		Name: x.Name(), Kind: x.Kind().Written(), Path: x.Path(), Description: x.Description(),
		Servers: []string{}, Tools: []string{}, BuiltinTools: []string{},
		Enabled: c.Enabled, Schedule: c.Schedule, Timeout: settings.DurationText(c.Timeout), Folder: c.WorkFolder,
	}
	if rd.ConfigErr == nil {
		if rd.TrustErr != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s: %v\n", x.Name(), rd.TrustErr)
		} else {
			row.Trusted, row.Untrusted = rd.Untrusted == "", rd.Untrusted
		}
	}
	if c.Schedule != "" {
		row.Staleness = settings.DurationText(c.Staleness)
		row.ScheduleText = schedule.Text(c.Schedule)
	}
	if c.Enabled && c.Schedule != "" {
		if n := scheduler.Next(c.Schedule, now); !n.IsZero() {
			row.Next = &n
		}
	}
	row.TurnOn = a.NeedsTurnOn(x)
	row.LastRun = status.LastRun(x.Name())
	// What only one kind has.
	if r, _, err := engine.AsRoutine(x, ""); err == nil {
		llmInfo(cmd, a, def, r, &row)
	}
	if p, err := engine.AsProgram(x); err == nil {
		row.Program = programInfo(a, p)
	}
	return row
}

// llmInfo adds what an LLM routine has: its servers, tools and model, and
// the settings the model backend takes.
func llmInfo(cmd *cobra.Command, a *engine.App, def engine.BackendDef, r routines.Routine, row *RoutineInfo) {
	row.Servers, row.Tools, row.Model = r.Servers, r.Tools, r.Model
	row.Session = "discard"
	if row.Servers == nil {
		row.Servers = []string{}
	}
	if row.Tools == nil {
		row.Tools = []string{}
	}
	c, err := a.RoutineConfig(r)
	if err == nil {
		row.Effort, row.Folder = c.Effort, c.Folder
		if c.SaveSession {
			row.Session = "save"
		}
		if c.BuiltinTools != nil {
			row.BuiltinTools = c.BuiltinTools
		}
	}
	if row.AgentModel, err = def.AgentModel(a, r.Model); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s: %v\n", r.Name, err)
	}
	if row.Effort != "default" {
		row.AgentEffort = row.Effort
	}
}

// programInfo is a program's own section.
func programInfo(a *engine.App, p programs.Program) *ProgramInfo {
	info := &ProgramInfo{Executable: p.Executable, Args: p.Args, Writes: p.Writes, Secrets: []ProgramSecret{}}
	if info.Args == nil {
		info.Args = []string{}
	}
	info.Checksum, _ = programs.Checksum(p.Executable)
	for _, s := range p.Secrets {
		key := engine.ProgramSecretKey(p.Name, s.Name)
		if p.Hook {
			key = engine.HookSecretKey(p.Name, s.Name)
		}
		_, stored, _ := a.Stored(engine.Secret{Key: key})
		info.Secrets = append(info.Secrets, ProgramSecret{Name: s.Name, Env: s.Env, Stored: stored})
	}
	return info
}

// warnBroken reports routines that couldn't be loaded (they're skipped).
func warnBroken(cmd *cobra.Command, broken []engine.Broken) {
	for _, b := range broken {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: skipping %v\n", b)
	}
}
