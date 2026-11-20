// Package usage is the LLM usage limit, which means what the backend's cost
// means: on a Claude plan (claude-code), how much of the plan's own usage
// limits is used, against llm.usage-limit; with an API key (api), what runs
// spent at list prices, against the llm.budget.* budgets. Past the limit,
// scheduled runs don't start.
package usage

import "time"

// Week is the length of the weekly window.
const Week = 7 * 24 * time.Hour

// Report is usage against the limit, for the backend in use.
type Report struct {
	// Kind is "plan" (claude-code: the Claude plan's usage against
	// llm.usage-limit) or "budget" (api: spend against llm.budget.*).
	Kind string `json:"kind" jsonschema:"enum=plan,enum=budget"`
	// LimitPercent is llm.usage-limit (plan).
	LimitPercent float64  `json:"limitPercent,omitempty"`
	Windows      []Window `json:"windows"`
	// FetchedAt is when the plan's usage was last read from Claude Code
	// (plan); ReadError why the last try failed, when it did (the last
	// reading still stands; with none, runs go ahead).
	FetchedAt *time.Time `json:"fetchedAt,omitempty"`
	ReadError string     `json:"readError,omitempty"`
	// RecordedAt is when Claude Code's status line last recorded the plan's
	// usage (plan; the fallback while /usage can't be read).
	RecordedAt *time.Time `json:"recordedAt,omitempty"`
	// Models are single models' weekly limits (plan; shown, not applied).
	Models []ModelUsage `json:"models,omitempty"`
	// Over is true when either window is past its limit; Reason says which.
	Over   bool   `json:"over"`
	Reason string `json:"reason,omitempty"`
}

// ModelUsage is one model's weekly limit.
type ModelUsage struct {
	Model       string    `json:"model"`
	UsedPercent float64   `json:"usedPercent"`
	ResetsAt    time.Time `json:"resetsAt"`
}

// Window is usage in one window.
type Window struct {
	// Name is "session" or "weekly" (plan), "window" or "week" (budget).
	Name string `json:"name"`
	// Length is the window's length ("5h", "7d").
	Length string `json:"length"`
	// UsedPercent is how much of the plan's limit is used, and ResetsAt when
	// it resets (plan; absent: not known).
	UsedPercent *float64   `json:"usedPercent,omitempty"`
	ResetsAt    *time.Time `json:"resetsAt,omitempty"`
	// SpentUSD is what runs spent in it, and BudgetUSD its budget (budget;
	// a budget of 0 is none).
	SpentUSD  *float64 `json:"spentUsd,omitempty"`
	BudgetUSD *float64 `json:"budgetUsd,omitempty"`
	Over      bool     `json:"over"`
}
