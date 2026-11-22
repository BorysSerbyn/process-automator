package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/history"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
)

// Finding is one problem doctor found, with what fixes it.
type Finding struct {
	// Area is what it's about: scheduler, usage, routine, hook or run.
	Area    string `json:"area" jsonschema:"enum=scheduler,enum=usage,enum=routine,enum=hook,enum=run"`
	Subject string `json:"subject"`
	Problem string `json:"problem"`
	Fix     string `json:"fix,omitempty"`
}

// DoctorOutput is `doctor --json`.
type DoctorOutput struct {
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
			out.OK = len(out.Findings) == 0
			switch {
			case wantsJSON(cmd):
				if err := writeJSON(cmd.OutOrStdout(), out); err != nil {
					return err
				}
			case out.OK:
				fmt.Fprintln(cmd.OutOrStdout(), "No problems found.")
			default:
				for _, f := range out.Findings {
					fmt.Fprintf(cmd.OutOrStdout(), "✗ %s (%s): %s\n", f.Subject, f.Area, f.Problem)
					if f.Fix != "" {
						fmt.Fprintf(cmd.OutOrStdout(), "    fix: %s\n", f.Fix)
					}
				}
			}
			switch n := len(out.Findings); n {
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
			add("routine", name, p.Text, p.Fix)
		}
		if rd.ConfigErr == nil {
			lastRunFinding(add, status, name, false)
		}
	}
	hookFindings(cmd, a, status, add)
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
func hookFindings(cmd *cobra.Command, a *engine.App, status *runstatus.Status, add func(area, subject, problem, fix string)) {
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
			add("hook", x.Name(), p.Text, p.Fix)
		}
		if rd.ConfigErr == nil {
			lastRunFinding(add, status, x.Name(), true)
		}
	}
}
