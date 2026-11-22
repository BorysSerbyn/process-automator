package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/mcp"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/programs"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/trigger"
	"github.com/BorysSerbyn/process-automator/core/internal/trust"
)

// A program: a routine that runs an executable instead of a model. This file
// is its side of Runnable. It has no tools to allowlist and no writes to
// intercept: trust by checksum is its guard, and what runs is the app's own
// copy of what was trusted.

// program is a program as the engine runs it: a program, or a hook's action
// (event: it's given the event it runs for on stdin).
type program struct {
	p     programs.Program
	event bool
}

// AsProgram is x as a program; an error for an LLM routine, for what only
// programs have.
func AsProgram(x Runnable) (programs.Program, error) {
	if _, ok := x.(*hook); ok {
		return programs.Program{}, fmt.Errorf("%s is a hook, not a program", x.Name())
	}
	p, ok := x.(*program)
	if !ok {
		return programs.Program{}, fmt.Errorf("%s is an LLM routine, not a program", x.Name())
	}
	return p.p, nil
}

func (p *program) Name() string                 { return p.p.Name }
func (p *program) Kind() Kind                   { return KindProgram }
func (p *program) Path() string                 { return p.p.Path }
func (p *program) Description() string          { return p.p.Description }
func (p *program) Source() string               { return p.p.Source }
func (p *program) Settings() []settings.Setting { return p.p.Settings() }

// runConfig: its schedule and timeout; it works in its data folder, skips the
// run queue (the queue is for the model's plan and usage limits), isn't held
// back by the LLM usage limit, and needs trust even for a dry run (one that
// doesn't write runs for real).
func (p *program) runConfig(a *App) (RunConfig, error) {
	c := RunConfig{WorkFolder: a.Layout.ProgramData(p.p.Name), DryRunsNeedTrust: true}
	if p.p.Hook {
		c.WorkFolder = a.Layout.HookData(p.p.Name)
	}
	vals, err := (settings.Store{Path: a.Layout.Config(), Extra: p.p.Settings()}).Values()
	if err != nil {
		return c, err
	}
	key := p.p.Key // a hook's are hooks.<name>.<field>
	c.Enabled = vals.Bool(key("enabled"))
	if c.Timeout, err = vals.Duration(key("timeout")); err != nil {
		return c, err
	}
	if !p.p.Scheduled() {
		return c, nil
	}
	c.Schedule = vals.String(key("schedule"))
	if c.Staleness, err = vals.Duration(key("staleness")); err != nil {
		return c, err
	}
	return c, nil
}

// ProgramSecretKey is the secret-store key of a program's secret: its own,
// namespaced by program.
func ProgramSecretKey(program, name string) string { return "program/" + program + "/" + name }

// HookSecretKey is the secret-store key of a program hook's secret.
func HookSecretKey(hook, name string) string { return "hook/" + hook + "/" + name }

// secret is one of its secrets as a sign-in asks for it.
func (p *program) secret(s programs.Secret) Secret {
	label := s.Prompt
	if label == "" {
		label = s.Name
	}
	key := ProgramSecretKey(p.p.Name, s.Name)
	if p.p.Hook {
		key = HookSecretKey(p.p.Name, s.Name)
	}
	return Secret{Key: key, Label: label}
}

// problems: none it can have that its file being loaded doesn't rule out (a
// secret that isn't stored is a sign-in, as an LLM routine's server's is:
// see check).
func (p *program) problems(*App) []Problem { return nil }

// check: a secret it declares that isn't stored, or can't be read.
func (p *program) check(_ context.Context, a *App, rd *Readiness) {
	for _, s := range p.p.Secrets {
		switch _, stored, err := a.Stored(p.secret(s)); {
		case err != nil:
			rd.Problems = append(rd.Problems, Problem{Text: fmt.Sprintf("its secret %s can't be read: %v", s.Name, err)})
		case !stored:
			rd.Problems = append(rd.Problems, Problem{Text: fmt.Sprintf("its secret %s isn't stored", s.Name), Fix: product.Command + " auth login " + p.p.Name})
		}
	}
}

func (p *program) reaches(*App) []string {
	if len(p.p.Secrets) == 0 {
		return nil
	}
	return []string{p.p.Name}
}

// approval: its executable and description file by checksum, and what the
// file declares.
func (p *program) approval(*App) (trust.Approval, error) {
	sum, err := programs.Checksum(p.p.Executable)
	if err != nil {
		return trust.Approval{}, fmt.Errorf("its executable can't be read: %w", err)
	}
	tp := &trust.Program{Executable: sum, Description: programs.ChecksumText(p.p.Source), Writes: p.p.Writes}
	for _, s := range p.p.Secrets {
		tp.Secrets = append(tp.Secrets, s.Name)
	}
	return trust.Approval{Program: tp}, nil
}

// run runs the app's copy of the executable, checked against what was
// trusted right before it starts, with its own environment: a fixed PATH,
// HOME, its secrets and what the run is for, in its data folder. On a dry
// run, one that writes gets --dry-run.
func (p *program) run(ctx context.Context, a *App, rc runContext) (*RunResult, error) {
	res := &RunResult{Kind: KindProgram}
	trusted, _, err := a.TrustStore().Load()
	if err != nil {
		return res, err
	}
	sum, err := programs.Checksum(p.p.Executable)
	if err != nil {
		return res, fmt.Errorf("its executable can't be read: %w", err)
	}
	if rec, ok := trusted.Routines[p.p.Name]; !ok || rec.Program == nil || rec.Program.Executable != sum {
		return res, errors.New("its executable isn't the one you trusted: review it with `" + product.Command + " routines trust " + p.p.Name + "`")
	}
	vars := map[string]string{
		programs.TriggerEnv: rc.occ.Trigger,
		programs.ForEnv:     rc.occ.At.Format(time.RFC3339),
		programs.RunIDEnv:   rc.id,
	}
	for _, s := range p.p.Secrets {
		v, stored, err := a.Stored(p.secret(s))
		switch {
		case err != nil:
			return res, fmt.Errorf("its secret %s can't be read: %w", s.Name, err)
		case !stored:
			return res, &mcp.NeedsReauthError{Server: p.p.Name, Cause: fmt.Errorf("its secret %s isn't stored", s.Name)}
		}
		vars[s.Env] = v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return res, err
	}
	if err := os.MkdirAll(rc.cfg.WorkFolder, 0o700); err != nil {
		return res, err
	}
	args := p.p.Args
	if rc.dryRun && p.p.Writes {
		args = append(append([]string{}, args...), programs.DryRunFlag)
	}
	inv := programs.Invocation{
		Executable: p.p.Executable, Args: args, Dir: rc.cfg.WorkFolder,
		Env: programs.Environment(home, vars), Log: rc.log,
	}
	if p.event {
		// A hook's program gets its event as JSON on stdin.
		in, err := eventFile(rc.occ)
		if err != nil {
			return res, err
		}
		defer func() { _ = os.Remove(in) }()
		inv.Stdin = in
	}
	out, err := programs.Exec(ctx, inv)
	if err != nil && ctx.Err() == nil && !p.p.Writes && slept(rc.started) {
		// The computer slept mid-run; one that changes nothing can run again.
		logTo(rc.log, "the computer slept during the run (%v); running it again", err)
		out, err = programs.Exec(ctx, inv)
	}
	if out != nil {
		out.DryRun, out.Checksum = rc.dryRun, sum
		res.Program, res.Note = out, out.Summary
		res.Line = fmt.Sprintf("exit %d", out.ExitCode)
		if out.Summary != "" {
			res.Line += " · " + out.Summary
		}
	}
	return res, err
}

// eventFile writes the event a hook's program runs for (one: a program hook
// doesn't batch) to a private temporary file, for its stdin.
func eventFile(occ trigger.Occurrence) (string, error) {
	evs := occurrenceEvents(occ)
	if len(evs) == 0 {
		return "", errors.New("no event to run for")
	}
	b, err := json.Marshal(evs[len(evs)-1].Map())
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp("", "hook-event-")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), f.Close()
}
