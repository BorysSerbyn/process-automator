package usage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/atomicfile"
)

// Plan is what's known of the Claude plan's usage (plan-usage.json): the
// last reading of Claude Code's /usage, and limits runs hit since. The app
// never works out a cost for this backend.
type Plan struct {
	Session *PlanWindow `json:"session,omitempty"` // the 5-hour limit
	Weekly  *PlanWindow `json:"weekly,omitempty"`  // the weekly limit, all models
	// Models are the weekly limits of single models (shown, never applied).
	Models []ModelWindow `json:"models,omitempty"`
	// FetchedAt is when /usage last answered; TriedAt when it was last asked
	// (so a failing /usage isn't asked again at once), and TryError why it
	// last failed ("" when it answered).
	FetchedAt *time.Time `json:"fetchedAt,omitempty"`
	TriedAt   *time.Time `json:"triedAt,omitempty"`
	TryError  string     `json:"tryError,omitempty"`
	// RecordedAt is when Claude Code's status line last recorded it (the
	// fallback while /usage can't be read).
	RecordedAt *time.Time `json:"recordedAt,omitempty"`
}

// PlanWindow is one of the plan's limits.
type PlanWindow struct {
	UsedPercent float64   `json:"usedPercent"`
	ResetsAt    time.Time `json:"resetsAt"`
}

// ModelWindow is one model's weekly limit.
type ModelWindow struct {
	Model string `json:"model"`
	PlanWindow
}

// Limit is one of the plan's limits as Claude Code's /usage reports it.
type Limit struct {
	// Kind is "session", "weekly_all" or "weekly_scoped" (one model: Model).
	Kind     string
	Model    string
	Percent  float64
	ResetsAt time.Time
}

// limitHitRetry is when to try again after a run hit a limit without saying
// when it resets.
const limitHitRetry = time.Hour

// LoadPlan reads the plan file; none yet is an empty plan.
func LoadPlan(path string) (*Plan, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Plan{}, nil
	}
	if err != nil {
		return nil, err
	}
	var p Plan
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &p, nil
}

// Refresh reads the plan's usage again with fetch when the last reading is
// older than maxAge (or there's none), and returns what's known. A fetch
// that fails keeps the last reading, and isn't retried before maxAge either.
// Concurrent processes share one fetch (the file's lock).
func Refresh(path string, maxAge time.Duration, now time.Time, fetch func() ([]Limit, error)) (*Plan, error) {
	var plan *Plan
	err := atomicfile.UpdateJSON(path, LoadPlan, func(p *Plan) error {
		plan = p
		last := p.TriedAt
		if last != nil && now.Sub(*last) < maxAge {
			return nil
		}
		p.TriedAt = &now
		limits, err := fetch()
		if err != nil {
			p.TryError = err.Error()
			return nil
		}
		p.FetchedAt, p.TryError = &now, ""
		p.Session, p.Weekly, p.Models = nil, nil, nil
		for _, l := range limits {
			w := PlanWindow{UsedPercent: l.Percent, ResetsAt: l.ResetsAt}
			switch l.Kind {
			case "session":
				p.Session = &w
			case "weekly_all":
				p.Weekly = &w
			case "weekly_scoped":
				p.Models = append(p.Models, ModelWindow{Model: l.Model, PlanWindow: w})
			}
		}
		return nil
	})
	return plan, err
}

// RecordStatusLine records the plan usage in Claude Code's status line input
// (rate_limits: five_hour and seven_day, used_percentage and resets_at).
// Input without them changes nothing.
func RecordStatusLine(path string, input []byte, now time.Time) error {
	var in struct {
		RateLimits struct {
			FiveHour *statusWindow `json:"five_hour"`
			SevenDay *statusWindow `json:"seven_day"`
		} `json:"rate_limits"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Errorf("status line input: %w", err)
	}
	five, seven := in.RateLimits.FiveHour.window(), in.RateLimits.SevenDay.window()
	if five == nil && seven == nil {
		return nil
	}
	return atomicfile.UpdateJSON(path, LoadPlan, func(p *Plan) error {
		if five != nil {
			p.Session = five
		}
		if seven != nil {
			p.Weekly = seven
		}
		p.RecordedAt = &now
		return nil
	})
}

type statusWindow struct {
	UsedPercentage *float64 `json:"used_percentage"`
	ResetsAt       *int64   `json:"resets_at"` // Unix seconds
}

func (s *statusWindow) window() *PlanWindow {
	if s == nil || s.UsedPercentage == nil || s.ResetsAt == nil {
		return nil
	}
	return &PlanWindow{UsedPercent: *s.UsedPercentage, ResetsAt: time.Unix(*s.ResetsAt, 0)}
}

// RecordLimitHit records that a run hit the plan's session or weekly limit:
// it's used up until resetsAt (zero: unknown, so it's tried again in an hour).
func RecordLimitHit(path, window string, resetsAt, now time.Time) error {
	if resetsAt.IsZero() {
		resetsAt = now.Add(limitHitRetry)
	}
	w := &PlanWindow{UsedPercent: 100, ResetsAt: resetsAt}
	return atomicfile.UpdateJSON(path, LoadPlan, func(p *Plan) error {
		if window == "weekly" {
			p.Weekly = w
		} else {
			p.Session = w
		}
		return nil
	})
}

// AssessPlan is the plan's usage against llm.usage-limit at now: the
// session and all-models weekly limits apply. A window whose reset has
// passed is back at 0%. Nothing known is never over: runs go ahead.
func AssessPlan(p *Plan, limitPercent float64, now time.Time) Report {
	r := Report{Kind: "plan", LimitPercent: limitPercent, FetchedAt: p.FetchedAt, ReadError: p.TryError, RecordedAt: p.RecordedAt}
	for _, w := range []struct {
		name, length, words string
		known               *PlanWindow
	}{{"session", "5h", "5-hour", p.Session}, {"weekly", "7d", "weekly", p.Weekly}} {
		win := Window{Name: w.name, Length: w.length}
		if k := w.known; k != nil {
			used, resets := k.UsedPercent, k.ResetsAt
			if !resets.After(now) {
				used = 0 // reset since the reading
			}
			win.UsedPercent, win.ResetsAt = &used, &resets
			win.Over = used >= 100 || used > limitPercent
			if win.Over && !r.Over {
				r.Over = true
				r.Reason = fmt.Sprintf("the Claude plan's %s usage is at %.0f%%, past llm.usage-limit (%g%%); it resets %s",
					w.words, used, limitPercent, resets.Local().Format("Mon 15:04"))
				if used >= 100 {
					r.Reason = fmt.Sprintf("the Claude plan's %s limit is reached; it resets %s", w.words, resets.Local().Format("Mon 15:04"))
				}
			}
		}
		r.Windows = append(r.Windows, win)
	}
	for _, m := range p.Models {
		used, resets := m.UsedPercent, m.ResetsAt
		if !resets.After(now) {
			used = 0
		}
		r.Models = append(r.Models, ModelUsage{Model: m.Model, UsedPercent: used, ResetsAt: resets})
	}
	return r
}
