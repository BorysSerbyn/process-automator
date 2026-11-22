package engine

import (
	"context"
	"slices"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
)

// Readiness is whether a routine of either kind can run, as far as can be
// told without running it. It's the one set of checks behind doctor, routines
// check, setup, the scheduler and routines list; each reports what it needs.
type Readiness struct {
	// Config is the routine's effective settings. ConfigErr says why they
	// don't resolve; then nothing else is checked.
	Config    RunConfig
	ConfigErr error
	// Untrusted says why it may not run for real, and how to fix that (""
	// when it may). TrustErr is a failure to read what was trusted.
	Untrusted string
	TrustErr  error
	// Problems keep it from running whether trusted or not: an LLM routine's
	// missing folder, built-in tools its backend doesn't have, a model whose
	// provider isn't set up, a server nothing provides and, once Check has
	// run, its tools, a sign-in or its model's server; once Check has run, a
	// program's secret that isn't stored; for any kind, a permission it needs
	// that isn't granted (or hasn't been checked yet).
	Problems []Problem
	// Tools are an LLM routine's allowlisted tools as a run gets them (Check).
	Tools []agent.CheckedTool

	trustFix string
}

// All is everything that keeps it from running for real, with the
// command that fixes each: its settings (then nothing else was checked),
// its problems, and why it isn't trusted.
func (rd Readiness) All() []Problem {
	if rd.ConfigErr != nil {
		return []Problem{{Text: rd.ConfigErr.Error()}}
	}
	all := slices.Clone(rd.Problems)
	switch {
	case rd.TrustErr != nil:
		all = append(all, Problem{Text: rd.TrustErr.Error()})
	case rd.Untrusted != "":
		all = append(all, Problem{Text: rd.Untrusted, Fix: rd.trustFix})
	}
	return all
}

// Problem is something that keeps a routine from running, with the
// command that fixes it ("" when its file or settings need editing).
type Problem struct {
	Text, Fix string
	// Permission is the permission it's about, when it's one that isn't
	// granted or hasn't been checked; when it isn't granted, Open is the
	// address of the OS's settings page that turns it on ("" where there's
	// none) and `permissions request <permission>` asks for it.
	Permission, Open string
}

// Assess checks what can be checked without connecting to anything: its
// settings resolve, what its kind checks (an LLM routine's folder, built-in
// tools, model and servers), and whether it's trusted.
func (a *App) Assess(x Runnable) Readiness {
	rd := Readiness{trustFix: TrustCommand(x)}
	if rd.Config, rd.ConfigErr = x.runConfig(a); rd.ConfigErr != nil {
		return rd
	}
	rd.Problems = append(x.problems(a), PermissionProblems(x.Name(), a.Permissions(x))...)
	rd.Untrusted, rd.TrustErr = a.Untrusted(x)
	return rd
}

// Check adds what connecting or the secret store finds: an LLM routine's
// servers and their tools, sign-ins and its model's server (see CheckTools,
// CheckModel); a program's secrets.
func (rd *Readiness) Check(ctx context.Context, a *App, x Runnable) {
	if rd.ConfigErr == nil {
		x.check(ctx, a, rd)
	}
}
