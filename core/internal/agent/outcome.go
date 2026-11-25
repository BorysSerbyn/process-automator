package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// A run's outcome is decided from these, strongest first:
//
//  1. a sign-in that failed during the run → needs re-authentication;
//  2. a write tool whose last attempt failed → failed, whatever the model says
//     (the routine's side effect didn't happen);
//  3. the routine's own report (report_outcome), held to its outcome
//     contract (automator.outcome, see Contract): done only once the writes
//     it declares each succeeded, nothing_to_do only citing one of the cases
//     it declares, failed otherwise. Not reporting at all is a failure: the
//     core can't tell it did its job.
//
// A backend error (the model or CLI failing, max turns, timeout) fails the run
// before any of this.

// Routine outcomes, as reported through report_outcome.
const (
	OutcomeDone        = "done"
	OutcomeNothingToDo = "nothing_to_do"
	OutcomeFailed      = "failed"
)

// OutcomeTool is the built-in tool every routine gets for reporting how it went.
const OutcomeTool = "report_outcome"

// Contract is a routine's outcome contract (automator.outcome): what a
// successful run is. The zero contract is a routine that declares none: it
// can report done or failed, never nothing_to_do.
type Contract struct {
	// Done says what success means, in a sentence.
	Done string `json:"done,omitempty"`
	// NothingToDo are the only cases in which there's legitimately nothing
	// to do; a nothing_to_do report must cite one, word for word.
	NothingToDo []string `json:"nothingToDo,omitempty"`
	// Writes are tools (allowlist names) done needs at least one successful
	// call of each (a dry run's intercepted call counts).
	Writes []string `json:"writes,omitempty"`
}

// Declared reports whether the routine declares a contract (its done).
func (c Contract) Declared() bool { return c.Done != "" }

// outcomes are the outcomes the contract lets a run report.
func (c Contract) outcomes() []string {
	if len(c.NothingToDo) == 0 {
		return []string{OutcomeDone, OutcomeFailed}
	}
	return []string{OutcomeDone, OutcomeNothingToDo, OutcomeFailed}
}

// Instructions are added to the routine's system prompt (every backend):
// how to report, in a few lines built from its contract.
func (c Contract) Instructions() string {
	if !c.Declared() {
		return "When you're finished, call " + OutcomeTool + ` once: "done" if you did what the routine asks, ` +
			`"failed" if you couldn't (a tool refused or errored, or its results look wrong or incomplete). Give the reason.`
	}
	var b strings.Builder
	b.WriteString("When you're finished, call " + OutcomeTool + " once:\n")
	fmt.Fprintf(&b, "- done: %s", strings.TrimSpace(c.Done))
	if len(c.Writes) > 0 {
		fmt.Fprintf(&b, " (needs a successful %s)", strings.Join(c.Writes, " and "))
	}
	switch len(c.NothingToDo) {
	case 0:
		b.WriteString("\n- nothing_to_do: never")
	case 1:
		fmt.Fprintf(&b, "\n- nothing_to_do: only if %s (give it as case)", c.NothingToDo[0])
	default:
		fmt.Fprintf(&b, "\n- nothing_to_do: only if one of these, given as case: %s", strings.Join(c.NothingToDo, "; "))
	}
	b.WriteString("\n- failed: anything else, including tool results that look wrong, stale or incomplete. When unsure, failed.")
	return b.String()
}

type reported struct {
	outcome, reason string
	// kase is the nothing-to-do case cited; evidence what the model gave as showing it (optional).
	kase, evidence string
}

// outcomeTool is the gateway tool behind report_outcome, offered as the
// contract allows. It's read-only, so dry-runs let it through. A report that
// breaks the contract is refused (a tool error), so the model can correct it.
func outcomeTool(res *Result, c Contract) Tool {
	props := map[string]any{
		"outcome":  map[string]any{"type": "string", "enum": c.outcomes()},
		"reason":   map[string]any{"type": "string", "description": "Why, in one sentence."},
		"evidence": map[string]any{"type": "string", "description": "Optional, short: what shows it (e.g. the message posted)."},
	}
	if len(c.NothingToDo) > 0 {
		props["case"] = map[string]any{"type": "string", "enum": c.NothingToDo,
			"description": "For nothing_to_do: which case."}
	}
	return Tool{
		Name:        OutcomeTool,
		Description: "Report how this routine run went. Call once, at the end.",
		InputSchema: map[string]any{"type": "object", "properties": props, "required": []string{"outcome", "reason"}},
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
		Call: func(_ context.Context, args map[string]any) (*sdk.CallToolResult, error) {
			rep := &reported{}
			rep.outcome, _ = args["outcome"].(string)
			rep.reason, _ = args["reason"].(string)
			rep.kase, _ = args["case"].(string)
			rep.evidence, _ = args["evidence"].(string)
			call := res.startCall(OutcomeTool, "", args, false)
			if msg := refusal(res, c, rep); msg != "" {
				res.endCall(call, callEnd{err: msg})
				return textResult(msg, true), nil
			}
			res.report(rep)
			res.endCall(call, callEnd{result: "Recorded."})
			return textResult("Recorded.", false), nil
		},
	}
}

// refusal is why a report breaks the contract ("" when it doesn't).
func refusal(res *Result, c Contract, r *reported) string {
	switch r.outcome {
	case OutcomeDone:
		if missing := res.missingWrites(c.Writes); len(missing) > 0 {
			return fmt.Sprintf("done needs a successful call of %s, and this run has none: if it couldn't be done, report failed and say why",
				strings.Join(missing, " and "))
		}
	case OutcomeNothingToDo:
		if len(c.NothingToDo) == 0 {
			return "this routine declares no nothing-to-do cases: report done or failed"
		}
		if !slices.Contains(c.NothingToDo, r.kase) {
			return fmt.Sprintf("nothing_to_do must give one of the routine's cases as case: %q. If none applies, report failed", c.NothingToDo)
		}
	case OutcomeFailed:
	default:
		return "outcome must be " + strings.Join(c.outcomes(), ", ")
	}
	return ""
}

// RoutineFailedError is a run that completed but didn't do its job.
type RoutineFailedError struct{ Reason string }

func (e *RoutineFailedError) Error() string { return e.Reason }

// verdict decides the run's outcome after the backend finished without
// error, holding the report to the contract.
func verdict(res *Result, c Contract) error {
	if res.reauth != nil {
		return res.reauth
	}
	last := map[string]Write{}
	var order []string
	for _, w := range res.Writes {
		if _, seen := last[w.Tool]; !seen {
			order = append(order, w.Tool)
		}
		last[w.Tool] = w
	}
	for _, tool := range order {
		if w := last[tool]; w.Error != "" {
			return &RoutineFailedError{Reason: fmt.Sprintf("%s failed: %s", tool, w.Error)}
		}
	}
	r := res.reported
	if r == nil && res.Output != nil {
		// A valid result in the routine's declared shape is its report: agent
		// CLIs asked for structured output end with it rather than a tool call.
		r = &reported{outcome: OutcomeDone}
		res.reported = r
	}
	switch {
	case r == nil:
		return &RoutineFailedError{Reason: "the routine ended without reporting an outcome"}
	case r.outcome == OutcomeFailed:
		return &RoutineFailedError{Reason: "the routine reported it failed: " + r.reason}
	case r.outcome == OutcomeNothingToDo && len(c.NothingToDo) == 0:
		return &RoutineFailedError{Reason: "the routine reported nothing to do, but declares no nothing-to-do cases: " + r.reason}
	case r.outcome == OutcomeNothingToDo && !slices.Contains(c.NothingToDo, r.kase):
		return &RoutineFailedError{Reason: "the routine reported nothing to do without citing one of its cases: " + r.reason}
	case r.outcome == OutcomeDone:
		if missing := res.missingWrites(c.Writes); len(missing) > 0 {
			return &RoutineFailedError{Reason: fmt.Sprintf("the routine reported done, but %s never succeeded (its outcome contract needs it): %s",
				strings.Join(missing, " and "), r.reason)}
		}
	}
	res.Outcome, res.Reason, res.Case, res.Evidence = r.outcome, r.reason, r.kase, r.evidence
	return nil
}

// missingWrites are the tools of writes without a successful call in the run
// so far (a dry run's intercepted call counts).
func (r *Result) missingWrites(writes []string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var missing []string
	for _, w := range writes {
		if !slices.ContainsFunc(r.Calls, func(c Call) bool { return c.Tool == w && c.ended && c.Error == "" }) {
			missing = append(missing, w)
		}
	}
	return missing
}
