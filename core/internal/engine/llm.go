package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/mcp"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/trust"
	"github.com/BorysSerbyn/process-automator/core/internal/usage"
)

// An LLM routine (a routine, unless a kind is named): a prompt file a model
// runs with tools from its servers. This file is its side of Runnable.

// llmRoutine is an LLM routine, with the set it's in (its partials).
type llmRoutine struct {
	r   routines.Routine
	set *routines.Set
}

// LLM is r as a Runnable; set is the routines folder it's in (its partials;
// nil when it won't run).
func (a *App) LLM(r routines.Routine, set *routines.Set) Runnable {
	if set == nil {
		set = &routines.Set{Partials: map[string]string{}}
	}
	return &llmRoutine{r: r, set: set}
}

// AsRoutine is x as an LLM routine, with its set; for a program, an error
// saying only LLM routines have what (memories, a model).
func AsRoutine(x Runnable, what string) (routines.Routine, *routines.Set, error) {
	l, ok := x.(*llmRoutine)
	if !ok {
		return routines.Routine{}, nil, fmt.Errorf("%s is a program: only LLM routines have %s", x.Name(), what)
	}
	return l.r, l.set, nil
}

// Routine is the installed LLM routine named name, with the set it's in (its
// partials); for a program's name, an error saying only LLM routines have
// what.
func (a *App) Routine(name, what string) (routines.Routine, *routines.Set, error) {
	x, err := a.Find(name)
	if err != nil {
		return routines.Routine{}, nil, err
	}
	return AsRoutine(x, what)
}

func (l *llmRoutine) Name() string                 { return l.r.Name }
func (l *llmRoutine) Kind() Kind                   { return KindLLM }
func (l *llmRoutine) Path() string                 { return l.r.Path }
func (l *llmRoutine) Description() string          { return l.r.Description }
func (l *llmRoutine) Source() string               { return l.r.Source }
func (l *llmRoutine) Settings() []settings.Setting { return l.r.Settings() }

func (l *llmRoutine) runConfig(a *App) (RunConfig, error) {
	c, err := a.RoutineConfig(l.r)
	return c.RunConfig, err
}

// problems: its folder exists, its built-in tools are the backend's, its
// model's provider is set up, and every server it uses (directly or through
// its plugins) is provided.
func (l *llmRoutine) problems(a *App) []Problem {
	r := l.r
	c, err := a.RoutineConfig(r)
	if err != nil {
		return nil
	}
	var out []Problem
	if folder := c.Folder; folder != "" {
		if info, err := os.Stat(folder); err != nil || !info.IsDir() {
			out = append(out, Problem{
				Text: fmt.Sprintf("its folder %s doesn't exist", folder),
				Fix:  fmt.Sprintf("create it, or `"+product.Command+" settings set routines.%s.folder <folder>`", r.Name),
			})
		}
	}
	if def, err := a.BackendDef(); err == nil {
		// Each agent CLI names its own built-in tools.
		if def.CheckBuiltinTools != nil && len(c.BuiltinTools) > 0 {
			if err := def.CheckBuiltinTools(c.BuiltinTools); err != nil {
				out = append(out, Problem{
					Text: fmt.Sprintf("on the %s backend, %v", def.Name, err),
					Fix:  fmt.Sprintf("`"+product.Command+" settings set routines.%s.builtinTools <tools>`, or another llm.backend", r.Name),
				})
			}
		}
		if def.ModelProblem != nil {
			if p := def.ModelProblem(a, r.Model); p != nil {
				out = append(out, *p)
			}
		}
	}
	for _, s := range a.Servers(r) {
		if !a.KnownServer(s) {
			out = append(out, Problem{Text: "it uses " + s + ", which no installed plugin or server provides"})
		}
	}
	return out
}

func (l *llmRoutine) check(ctx context.Context, a *App, rd *Readiness) {
	rd.CheckTools(ctx, a, l.r)
	rd.CheckModel(ctx, a, l.r)
}

func (l *llmRoutine) reaches(a *App) []string { return a.Servers(l.r) }

func (l *llmRoutine) approval(a *App) (trust.Approval, error) {
	c, err := a.RoutineConfig(l.r)
	if err != nil {
		return trust.Approval{}, err
	}
	s := SurfaceOf(l.r, c)
	return trust.Approval{Surface: &s}, nil
}

// SurfaceOf is what an LLM routine can reach, with its effective settings.
func SurfaceOf(r routines.Routine, c RoutineConfig) trust.Surface {
	return trust.Surface{Servers: r.Servers, Tools: r.Tools, BuiltinTools: c.BuiltinTools, Folder: c.Folder, Plugins: r.Plugins}
}

// CheckTools connects to the routine's servers and confirms each allowlisted
// tool is offered by exactly one of them, as a run would, and that the
// servers it reaches through its plugins are signed in. It's skipped when a
// server is missing.
func (rd *Readiness) CheckTools(ctx context.Context, a *App, r routines.Routine) {
	if rd.ConfigErr != nil || slices.ContainsFunc(a.Servers(r), func(s string) bool { return !a.KnownServer(s) }) {
		return
	}
	tools, err := agent.CheckTools(ctx, a.Connector, RunOf(r, rd.Config.WorkFolder, ""), r.Servers, r.Tools)
	rd.Tools = tools
	if err != nil {
		fix := ""
		var nr *mcp.NeedsReauthError
		if errors.As(err, &nr) {
			fix = product.Command + " auth login " + nr.Server
		}
		rd.Problems = append(rd.Problems, Problem{Text: err.Error(), Fix: fix})
	}
	// Servers it reaches through its plugins aren't connected until a tool
	// needs them: check they're signed in.
	for _, s := range a.Servers(r) {
		if slices.Contains(r.Servers, s) {
			continue
		}
		if in, signs := a.SignedIn(s); signs && !in {
			rd.Problems = append(rd.Problems, Problem{Text: s + " (which it uses through a plugin) isn't signed in", Fix: product.Command + " auth login " + s})
		}
	}
}

// CheckModel connects to the server that runs the routine's model, on a
// backend that has one to reach (the local server), and adds why the model
// can't run there. It's skipped when Assess found its provider isn't set up.
func (rd *Readiness) CheckModel(ctx context.Context, a *App, r routines.Routine) {
	def, err := a.BackendDef()
	if rd.ConfigErr != nil || err != nil || def.ReachModel == nil {
		return
	}
	if def.ModelProblem != nil && def.ModelProblem(a, r.Model) != nil {
		return
	}
	if p := def.ReachModel(ctx, a, r.Model); p != nil {
		rd.Problems = append(rd.Problems, *p)
	}
}

// run runs the routine on the model backend, with its memories, inputs and
// tools.
func (l *llmRoutine) run(ctx context.Context, a *App, rc runContext) (*RunResult, error) {
	r := l.r
	res := &RunResult{Kind: KindLLM, Inputs: routines.Inputs(rc.occ)}
	cfg, err := a.RoutineConfig(r)
	if err != nil {
		return res, err
	}
	mem, err := a.Memories().Render(r.Name)
	if err != nil {
		return res, fmt.Errorf("reading memories: %w", err)
	}
	backend, err := a.NewBackend()
	if err != nil {
		return res, err
	}
	// Dry runs never keep a session: it would sit among my own agent CLI
	// sessions. A kept session's ID is on record while the run runs when the
	// app picks it (see agent.SessionPicker), from its end when the CLI does.
	saveSession := cfg.SaveSession && !rc.dryRun
	sessionID := ""
	if saveSession && !agent.PicksSessionID(backend) {
		sessionID = newSessionID()
	}
	rc.note(func(run *runstatus.Run) { run.Backend, run.SessionID = backend.Name(), sessionID })
	if cfg.WorkFolder != cfg.Folder { // the routine's own: made on its first run
		if err := os.MkdirAll(cfg.WorkFolder, 0o700); err != nil {
			return res, err
		}
	}
	ag := &agent.Agent{Connector: a.Connector, Backend: backend, DryRun: rc.dryRun, Log: rc.log}
	attempt := func() (*agent.Result, error) {
		return ag.Run(ctx, agent.Request{
			Name: r.Name, Source: r.Source, Origin: r.Path,
			Input: withParams(res.Inputs, cfg.Params), Tools: r.Tools, Servers: r.Servers,
			Run:      RunOf(r, cfg.WorkFolder, rc.id),
			Partials: l.set.PromptPartials(mem),
			Effort:   cfg.Effort,
			Folder:   cfg.WorkFolder, SaveSession: saveSession, SessionID: sessionID, BuiltinTools: cfg.BuiltinTools,
		})
	}
	out, err := attempt()
	if err != nil && ctx.Err() == nil && slept(rc.started) && safeToRetry(out, cfg.BuiltinTools, err) {
		// The computer slept mid-run (a closed lid, a maintenance wake
		// ending): its connections died with it. Nothing was changed yet,
		// so it runs again from the start, once.
		logTo(rc.log, "the computer slept during the run (%v); running it again", err)
		if saveSession && !agent.PicksSessionID(backend) {
			sessionID = newSessionID() // the first attempt's may be taken
			rc.note(func(run *runstatus.Run) { run.SessionID = sessionID })
		}
		out, err = attempt()
	}
	var limit *agent.PlanLimitError
	if errors.As(err, &limit) {
		if rerr := usage.RecordLimitHit(a.Layout.PlanUsage(), limit.Window, limit.ResetsAt, time.Now()); rerr != nil {
			logTo(rc.log, "warning: recording the Claude plan limit: %v", rerr)
		}
	}
	if out != nil && out.SessionID != "" {
		rc.note(func(run *runstatus.Run) { run.SessionID = out.SessionID }) // the one the CLI kept
	}
	res.Agent = out
	if out != nil {
		res.Line = out.Summary()
		if out.Outcome == agent.OutcomeNothingToDo {
			res.Note = "nothing to do: " + out.Reason
		}
	}
	return res, err
}

// safeToRetry is whether a failed run can run again from the start without
// doing anything twice: it made no changes through its tools, can't have made
// any through the agent CLI's own (it has none), and didn't fail on something
// a retry can't fix (a sign-in, the plan's limit).
func safeToRetry(res *agent.Result, builtinTools []string, err error) bool {
	var limit *agent.PlanLimitError
	switch {
	case len(builtinTools) > 0, errors.Is(err, mcp.ErrNeedsReauth), errors.As(err, &limit):
		return false
	case res == nil:
		return true
	default:
		return len(res.Writes) == 0
	}
}

// Execute runs an LLM routine (see App.Run) and returns what the model did.
func (a *App) Execute(ctx context.Context, set *routines.Set, r routines.Routine, o RunOptions) (*agent.Result, error) {
	res, err := a.Run(ctx, a.LLM(r, set), o)
	if res == nil {
		return nil, err
	}
	return res.Agent, err
}

// InitTrust trusts, the first time trust is used, every LLM routine installed
// then: routines from before trust existed keep running (programs came
// after it, so none is trusted this way). Edits, real runs and the scheduler
// call it first, so a routine made afterwards is never trusted this way;
// read-only commands don't (TrustProblem reads as if it had run).
func (a *App) InitTrust() error {
	if _, ok, err := a.TrustStore().Load(); ok || err != nil {
		return err
	}
	set, err := routines.Load(a.Layout.Routines())
	if err != nil {
		return err
	}
	current := map[string]trust.Approval{}
	for _, r := range set.Routines {
		if c, err := a.RoutineConfig(r); err == nil {
			s := SurfaceOf(r, c)
			current[r.Name] = trust.Approval{Surface: &s}
		}
	}
	return a.TrustStore().Init(current, time.Now())
}
