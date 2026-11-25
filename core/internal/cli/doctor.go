package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/history"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
)

// Finding is one problem doctor found, with what fixes it.
type Finding struct {
	// Area is what it's about: scheduler, usage, notifications, routine, hook or run.
	Area    string `json:"area" jsonschema:"enum=scheduler,enum=usage,enum=notifications,enum=routine,enum=hook,enum=run"`
	Subject string `json:"subject"`
	Problem string `json:"problem"`
	Fix     string `json:"fix,omitempty"`
	// Permission is the permission it's about when one isn't granted
	// (`permissions request <permission>` asks the OS for it), and Open the
	// address of the OS's page that turns it on (absent when there's none).
	Permission string `json:"permission,omitempty"`
	Open       string `json:"open,omitempty"`
	// Warning is a finding that doesn't make the status red, nor doctor
	// fail, but wants fixing (an LLM routine without an outcome contract).
	Warning bool `json:"warning,omitempty"`
}

// DoctorOutput is `doctor --json`.
type DoctorOutput struct {
	// OK is that there are no findings but warnings.
	OK       bool      `json:"ok"`
	Findings []Finding `json:"findings"`
}

func newDoctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Explain a red indicator: check the scheduler, enabled routines, their plugins and last runs",
		Long: "Checks what can make the status red, without calling the model or any tool: the scheduler is " +
			"running; each enabled routine loads, its settings resolve, its folder exists, it's trusted, its " +
			"servers connect (signed in) and offer its tools, its model's provider is set up (the local " +
			"server answers and has the model) or, for a program, its secrets are stored; and its last run didn't fail. Hooks that are on " +
			"too: and their watchers are there, trusted and working. Each problem comes " +
			"with what fixes it. Fails if there are any.",
		Annotations: readOnly(),
		Args:        cobra.NoArgs,
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, _ []string) error {
			out := DoctorOutput{Findings: diagnose(cmd, a, time.Now())}
			n := 0
			for _, f := range out.Findings {
				if !f.Warning {
					n++
				}
			}
			out.OK = n == 0
			if wantsJSON(cmd) {
				if err := writeJSON(cmd.OutOrStdout(), out); err != nil {
					return err
				}
			} else {
				if out.OK {
					fmt.Fprintln(cmd.OutOrStdout(), "No problems found.")
				}
				for _, f := range out.Findings {
					mark := "✗"
					if f.Warning {
						mark = "!"
					}
					fmt.Fprintf(cmd.OutOrStdout(), "%s %s (%s): %s\n", mark, f.Subject, f.Area, f.Problem)
					if f.Fix != "" {
						fmt.Fprintf(cmd.OutOrStdout(), "    fix: %s\n", f.Fix)
					}
				}
			}
			switch n {
			case 0:
			case 1:
				return reported(errors.New("1 problem"))
			default:
				return reported(fmt.Errorf("%d problems", n))
			}
			return nil
		}),
	}
	jsonFlag(cmd.Flags(), DoctorOutput{})
	return cmd
}

// diagnose finds what can turn the status red.
func diagnose(cmd *cobra.Command, a *engine.App, now time.Time) []Finding {
	findings := []Finding{}
	add := func(area, subject, problem, fix string) {
		findings = append(findings, Finding{Area: area, Subject: subject, Problem: problem, Fix: fix})
	}
	addProblem := func(area, subject string, p engine.Problem) {
		findings = append(findings, Finding{Area: area, Subject: subject, Problem: p.Text, Fix: p.Fix, Permission: p.Permission, Open: p.Open})
	}

	status, err := runstatus.Load(a.Layout.RunStatus())
	switch {
	case err != nil:
		add("scheduler", "run status", err.Error(), "")
	case status.ValidUntil == nil || now.After(*status.ValidUntil):
		add("scheduler", "scheduler", "isn't running, or stopped responding (run status isn't being refreshed)",
			"`"+product.Command+" login-items start` (opening "+product.Name+" runs it too); if it keeps stopping, `"+
				product.Command+" daemon` in a terminal shows why")
	case status.Problem != "":
		add("scheduler", "scheduler", status.Problem, "")
	}

	if status != nil {
		for _, w := range status.Waiting {
			add("notifications", w.Title, "waits for me to acknowledge it",
				"acknowledge it from its notification or the app, or `"+product.Command+" notifications ack "+w.ID+"`")
		}
	}

	if r, err := a.Usage(cmd.Context(), now); err != nil {
		add("usage", "LLM usage limit", err.Error(), "")
	} else if r.Over {
		fix := "wait for spend to fall out of the window, or raise the llm.budget.* budgets"
		if r.Kind == "plan" {
			fix = "wait for the plan's usage to reset, or raise llm.usage-limit"
		}
		add("usage", "LLM usage limit", "scheduled runs don't start: "+r.Reason, fix)
	}

	all, broken, err := a.All()
	if err != nil {
		add("routine", "routines folder", err.Error(), "")
		return findings
	}
	for _, b := range broken {
		add("routine", b.Path, b.Err.Error(), "fix the file, or `"+product.Command+" routines delete "+b.Name+"` it")
	}
	for _, x := range all {
		rd := a.Assess(x)
		if rd.ConfigErr == nil && !rd.Config.Enabled {
			continue // can't make anything red
		}
		rd.Check(cmd.Context(), a, x)
		name := x.Name()
		for _, p := range rd.All() {
			addProblem("routine", name, p)
		}
		for _, w := range rd.Warnings {
			findings = append(findings, Finding{Area: "routine", Subject: name, Problem: w, Warning: true,
				Fix: "add " + routines.Namespace + ".outcome to its file (`" + product.Command + " guide routines`), then `" +
					product.Command + " routines update " + name + " --from <file>`"})
		}
		if rd.ConfigErr == nil {
			lastRunFinding(add, status, name, false)
		}
	}
	hookFindings(cmd, a, status, add, addProblem, func(f Finding) { findings = append(findings, f) })
	return findings
}

// lastRunFinding adds a routine's or hook's last run, when it makes the
// status red (skipped: a hook's, paused past its rate; a routine's is the
// usage limit's finding).
func lastRunFinding(add func(area, subject, problem, fix string), status *runstatus.Status, name string, skipped bool) {
	run := status.LastRun(name)
	if run == nil {
		return
	}
	if run.Outcome == runstatus.OutcomeSkipped && !skipped {
		return
	}
	switch run.Outcome {
	case runstatus.OutcomeFailed, runstatus.OutcomeMissed, runstatus.OutcomeSkipped:
		add("run", name, fmt.Sprintf("its last run %s: %s", run.Outcome, run.Reason),
			"see it: "+product.Command+" runs show "+history.RunID(name, run.StartedAt))
	case runstatus.OutcomeNeedsReauth:
		add("run", name, "its last run needed a sign-in: "+run.Reason, product.Command+" auth status shows which one")
	}
}

// hookFindings adds what keeps each hook that's on from running.
func hookFindings(cmd *cobra.Command, a *engine.App, status *runstatus.Status, add func(area, subject, problem, fix string), addProblem func(area, subject string, p engine.Problem), warn func(Finding)) {
	all, broken, err := a.Hooks()
	if err != nil {
		add("hook", "hooks folder", err.Error(), "")
		return
	}
	for _, b := range broken {
		add("hook", b.Path, b.Err.Error(), "fix it, or `"+product.Command+" hooks delete "+b.Name+"` it")
	}
	for _, x := range all {
		rd := a.Assess(x)
		if rd.ConfigErr == nil && !rd.Config.Enabled {
			continue
		}
		rd.Check(cmd.Context(), a, x)
		for _, p := range rd.All() {
			addProblem("hook", x.Name(), p)
		}
		for _, w := range rd.Warnings {
			warn(Finding{Area: "hook", Subject: x.Name(), Problem: w, Warning: true,
				Fix: "add " + routines.Namespace + ".outcome to its prompt (`" + product.Command + " guide hooks`), then `" +
					product.Command + " hooks update " + x.Name() + " --from <file>`"})
		}
		if rd.ConfigErr == nil {
			lastRunFinding(add, status, x.Name(), true)
		}
	}
}
