package engine

import (
	"context"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/history"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/usage"
)

// PlanUsageMaxAge is how old a reading of the Claude plan's usage may be
// before it's read again (Claude Code's usage source is itself rate limited).
const PlanUsageMaxAge = 10 * time.Minute

// Usage is the LLM usage limit now, as the backend in use means it: the
// Claude plan's usage against llm.usage-limit (claude-code, read from Claude
// Code when the last reading is older than PlanUsageMaxAge), API spend
// against the llm.budget.* budgets (claude-api), or none (a backend with neither).
func (a *App) Usage(ctx context.Context, now time.Time) (usage.Report, error) {
	vals, err := a.Settings()
	if err != nil {
		return usage.Report{}, err
	}
	def, ok := Backends[vals.String("llm.backend")]
	switch {
	case !ok || def.Priced:
		return apiBudget(a, vals, now)
	case def.PlanUsage == nil:
		return usage.Report{Kind: usage.KindNone, Windows: []usage.Window{}}, nil
	}
	plan, err := usage.Refresh(a.Layout.PlanUsage(), PlanUsageMaxAge, now, func() ([]usage.Limit, error) {
		return def.PlanUsage(ctx, a)
	})
	if err != nil {
		return usage.Report{}, err
	}
	return usage.AssessPlan(plan, vals.Float("llm.usage-limit"), now), nil
}

// apiBudget is what claude-api runs spent, from the run history, against the budgets.
func apiBudget(a *App, vals settings.Values, now time.Time) (usage.Report, error) {
	b := usage.Budgets{WindowUSD: vals.Float("llm.budget.window-usd"), WeeklyUSD: vals.Float("llm.budget.weekly-usd")}
	var err error
	if b.Window, err = vals.Duration("llm.budget.window"); err != nil {
		return usage.Report{}, err
	}
	list, err := history.List(a.Layout.History(), history.Filter{Since: now.Add(-max(usage.Week, b.Window))})
	if err != nil {
		return usage.Report{}, err
	}
	var runs []usage.Run
	for _, s := range list {
		if s.Backend == "claude-api" || s.Backend == "api" { // api: its former name, in older runs
			runs = append(runs, usage.Run{StartedAt: s.StartedAt, CostUSD: s.CostUSD})
		}
	}
	return usage.AssessBudget(runs, b, now), nil
}
