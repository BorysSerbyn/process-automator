package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/mcp"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
)

// Readiness is whether a routine can run, as far as can be told without
// running it. It's the one set of checks behind doctor, routines check,
// setup, the scheduler and routines list; each reports what it needs.
type Readiness struct {
	// Config is the routine's effective settings. ConfigErr says why they
	// don't resolve; then nothing else is checked.
	Config    RoutineConfig
	ConfigErr error
	// Untrusted says why it may not run for real, and how to fix that (""
	// when it may). TrustErr is a failure to read what was trusted.
	Untrusted string
	TrustErr  error
	// Problems keep it from running whether trusted or not: a missing
	// folder, built-in tools its backend doesn't have, a model whose
	// provider isn't set up, a server nothing provides and, once CheckTools
	// and CheckModel have run, its tools, a sign-in or its model's server.
	Problems []Problem
	// Tools are its allowlisted tools as a run gets them (CheckTools).
	Tools []agent.CheckedTool
}

// Problem is something that keeps a routine from running, with the
// command that fixes it ("" when its file or settings need editing).
type Problem struct {
	Text, Fix string
}

// Assess checks what can be checked without connecting to anything:
// its settings resolve, its folder exists, its built-in tools are the
// backend's, its model's provider is set up, every server it uses (directly
// or through its plugins) is provided, and whether it's trusted.
func (a *App) Assess(r routines.Routine) Readiness {
	var rd Readiness
	if rd.Config, rd.ConfigErr = a.RoutineConfig(r); rd.ConfigErr != nil {
		return rd
	}
	if folder := rd.Config.Folder; folder != "" {
		if info, err := os.Stat(folder); err != nil || !info.IsDir() {
			rd.Problems = append(rd.Problems, Problem{
				Text: fmt.Sprintf("its folder %s doesn't exist", folder),
				Fix:  fmt.Sprintf("create it, or `"+product.Command+" settings set routines.%s.folder <folder>`", r.Name),
			})
		}
	}
	if def, err := a.BackendDef(); err == nil {
		// Each agent CLI names its own built-in tools.
		if def.CheckBuiltinTools != nil && len(rd.Config.BuiltinTools) > 0 {
			if err := def.CheckBuiltinTools(rd.Config.BuiltinTools); err != nil {
				rd.Problems = append(rd.Problems, Problem{
					Text: fmt.Sprintf("on the %s backend, %v", def.Name, err),
					Fix:  fmt.Sprintf("`"+product.Command+" settings set routines.%s.builtinTools <tools>`, or another llm.backend", r.Name),
				})
			}
		}
		if def.ModelProblem != nil {
			if p := def.ModelProblem(a, r.Model); p != nil {
				rd.Problems = append(rd.Problems, *p)
			}
		}
	}
	for _, s := range a.Servers(r) {
		if !a.KnownServer(s) {
			rd.Problems = append(rd.Problems, Problem{Text: "it uses " + s + ", which no installed plugin or server provides"})
		}
	}
	rd.Untrusted, rd.TrustErr = a.Untrusted(r, rd.Config)
	return rd
}

// CheckTools connects to the routine's servers and confirms each allowlisted
// tool is offered by exactly one of them, as a run would, and that the
// servers it reaches through its plugins are signed in (signedIn: by server,
// for those that sign in). It's skipped when a server is missing.
func (rd *Readiness) CheckTools(ctx context.Context, a *App, r routines.Routine, signedIn map[string]bool) {
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
		if in, signs := signedIn[s]; signs && !in && !slices.Contains(r.Servers, s) {
			rd.Problems = append(rd.Problems, Problem{Text: s + " (which it uses through a plugin) isn't signed in", Fix: product.Command + " auth login " + s})
		}
	}
}

// CheckModel connects to the server that runs the routine's model, on a
// backend that has one to reach (the local server),
// and adds why the model can't run there. It's skipped when Assess found its
// provider isn't set up.
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
