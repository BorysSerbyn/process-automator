package agent

import (
	"context"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// A run's outcome is decided from three things, strongest first:
//
//  1. a sign-in that failed during the run → needs re-authentication;
//  2. a write tool whose last attempt failed → failed, whatever the model says
//     (the routine's side effect didn't happen);
//  3. the routine's own report (report_outcome): done, nothing_to_do or failed.
//     Not reporting at all is a failure: the core can't tell it did its job.
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

// outcomeInstructions are added to every routine's system prompt.
const outcomeInstructions = "When you're finished, call " + OutcomeTool + " exactly once: " +
	`"done" if you did what the routine asks, "nothing_to_do" if there was legitimately nothing to do, ` +
	`"failed" if you couldn't do it (including when a tool refused or errored). Always give the reason. ` +
	"Then give your final reply."

type reported struct {
	outcome, reason string
}

// outcomeTool is the gateway tool behind report_outcome. It's read-only, so
// dry-runs let it through.
func outcomeTool(res *Result) Tool {
	return Tool{
		Name:        OutcomeTool,
		Description: "Report how this routine run went. Call exactly once, at the end.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"outcome": map[string]any{"type": "string", "enum": []string{OutcomeDone, OutcomeNothingToDo, OutcomeFailed}},
				"reason":  map[string]any{"type": "string", "description": "Why, in one sentence."},
			},
			"required": []string{"outcome", "reason"},
		},
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
		Call: func(_ context.Context, args map[string]any) (*sdk.CallToolResult, error) {
			outcome, _ := args["outcome"].(string)
			reason, _ := args["reason"].(string)
			switch outcome {
			case OutcomeDone, OutcomeNothingToDo, OutcomeFailed:
			default:
				return textResult("outcome must be done, nothing_to_do or failed", true), nil
			}
			res.report(&reported{outcome, reason})
			return textResult("Recorded.", false), nil
		},
	}
}

// RoutineFailedError is a run that completed but didn't do its job.
type RoutineFailedError struct{ Reason string }

func (e *RoutineFailedError) Error() string { return e.Reason }

// verdict decides the run's outcome after the backend finished without error.
func verdict(res *Result) error {
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
	}
	res.Outcome, res.Reason = r.outcome, r.reason
	return nil
}
