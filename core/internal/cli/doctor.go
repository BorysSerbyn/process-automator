package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/history"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
)

// Finding is one problem doctor found, with what fixes it.
type Finding struct {
	// Area is what it's about: scheduler, usage, routine or run.
	Area    string `json:"area" jsonschema:"enum=scheduler,enum=usage,enum=routine,enum=run"`
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
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Explain a red indicator: check the scheduler, enabled routines, their plugins and last runs",
		Long: "Checks what can make the status red, without calling the model or any tool: the scheduler is " +
			"running; each enabled routine loads, its settings resolve, its folder exists, it's trusted, its " +
			"servers connect (signed in) and offer its tools; and its last run didn't fail. Each problem comes " +
			"with what fixes it. Fails if there are any.",
		Annotations: readOnly(),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := loadApp(cmd)
			if err != nil {
				return err
			}
			out := DoctorOutput{Findings: diagnose(cmd, a, time.Now())}
			out.OK = len(out.Findings) == 0
			switch {
			case asJSON:
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
		},
	}
	jsonFlag(cmd.Flags(), &asJSON, DoctorOutput{})
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
			"start Process Automator, which starts the scheduler at login; `procauto daemon` in a terminal shows why it stops")
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

	set, err := routines.Load(a.Layout.Routines())
	if err != nil {
		add("routine", "routines folder", err.Error(), "")
		return findings
	}
	signIns := signedIn(a)
	for _, e := range set.Errors {
		add("routine", e.Path, e.Err.Error(), "fix the file, or `procauto routines delete` it")
	}
	for _, r := range set.Routines {
		rd := a.Assess(r)
		if rd.ConfigErr != nil {
			add("routine", r.Name, rd.ConfigErr.Error(), "")
			continue
		}
		if !rd.Config.Enabled {
			continue // can't make anything red
		}
		switch {
		case rd.TrustErr != nil:
			add("routine", r.Name, rd.TrustErr.Error(), "")
		case rd.Untrusted != "":
			add("routine", r.Name, rd.Untrusted, "procauto routines trust "+r.Name)
		}
		rd.CheckTools(cmd.Context(), a, r, signIns)
		for _, p := range rd.Problems {
			add("routine", r.Name, p.Text, p.Fix)
		}
		if status == nil {
			continue
		}
		for _, rs := range status.Routines {
			if rs.Name != r.Name || rs.LastRun == nil {
				continue
			}
			run := rs.LastRun
			id := history.RunID(r.Name, run.StartedAt)
			switch run.Outcome {
			case runstatus.OutcomeFailed, runstatus.OutcomeMissed:
				add("run", r.Name, fmt.Sprintf("its last run %s: %s", run.Outcome, run.Reason), "see it: procauto runs show "+id)
			case runstatus.OutcomeNeedsReauth:
				add("run", r.Name, "its last run needed a sign-in: "+run.Reason, "procauto auth status shows which one")
			}
		}
	}
	return findings
}
