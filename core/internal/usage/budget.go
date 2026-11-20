package usage

import (
	"fmt"
	"time"
)

// Budgets are the llm.budget.* settings (the api backend).
type Budgets struct {
	// Window is the short window's length.
	Window time.Duration
	// WindowUSD and WeeklyUSD are the budgets; 0 means no budget for that window.
	WindowUSD, WeeklyUSD float64
}

// Run is a run's start and cost.
type Run struct {
	StartedAt time.Time
	CostUSD   float64
}

// AssessBudget sums each window's spend at now.
func AssessBudget(runs []Run, b Budgets, now time.Time) Report {
	r := Report{Kind: "budget"}
	for _, w := range []struct {
		name   string
		length time.Duration
		budget float64
	}{{"window", b.Window, b.WindowUSD}, {"week", Week, b.WeeklyUSD}} {
		spent, budget := 0.0, w.budget
		for _, run := range runs {
			if now.Sub(run.StartedAt) < w.length {
				spent += run.CostUSD
			}
		}
		win := Window{Name: w.name, Length: Length(w.length), SpentUSD: &spent, BudgetUSD: &budget}
		win.Over = budget > 0 && spent > budget
		if win.Over && !r.Over {
			r.Over = true
			r.Reason = fmt.Sprintf("LLM spend in the last %s is $%.2f, past its $%.2f budget (llm.budget.*)",
				win.Length, spent, budget)
		}
		r.Windows = append(r.Windows, win)
	}
	return r
}

// Length is a window's length as people write it: "5h", "90m", "7d".
func Length(d time.Duration) string {
	switch {
	case d%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	default:
		return d.String()
	}
}
