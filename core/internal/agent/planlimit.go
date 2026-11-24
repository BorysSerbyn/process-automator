package agent

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// PlanLimitError is a claude-code run that hit the Claude plan's usage limit
// (Claude Code: "You've hit your session limit · resets 3:45pm").
type PlanLimitError struct {
	// Window is "session" (the 5-hour limit) or "weekly".
	Window string
	// ResetsAt is when the limit resets; zero when Claude Code didn't say.
	ResetsAt time.Time
	Message  string
}

func (e *PlanLimitError) Error() string { return "claude: " + e.Message }

var planLimit = regexp.MustCompile(`(?i)you've hit your (session|weekly) limit(?:\s*·\s*resets\s+(.+))?`)

// planLimitError reads a plan-limit message, or returns nil for any other
// (Claude Code 2.1.331's wording: claudeCode2_1_331.PlanLimit).
func planLimitError(msg string, now time.Time) *PlanLimitError {
	m := planLimit.FindStringSubmatch(msg)
	if m == nil {
		return nil
	}
	return &PlanLimitError{Window: strings.ToLower(m[1]), ResetsAt: resetTime(strings.TrimSpace(m[2]), now), Message: strings.TrimSpace(msg)}
}

// resetTime reads Claude Code's reset time ("3:45pm", "3pm", "Mon 12:00am",
// "Nov 23 4pm") as the next such moment after now, local time; zero when it
// can't.
func resetTime(text string, now time.Time) time.Time {
	text = strings.TrimSpace(strings.TrimRight(text, ".)"))
	if i := strings.Index(text, " ("); i >= 0 { // e.g. "3pm (Europe/London)"
		text = text[:i]
	}
	now = now.Local()
	at := func(t time.Time, y int, mo time.Month, d int) time.Time {
		return time.Date(y, mo, d, t.Hour(), t.Minute(), 0, 0, time.Local)
	}
	for _, layout := range []string{"3:04pm", "3pm"} {
		if t, err := time.Parse(layout, strings.ToLower(text)); err == nil {
			r := at(t, now.Year(), now.Month(), now.Day())
			if !r.After(now) {
				r = r.AddDate(0, 0, 1)
			}
			return r
		}
	}
	if day, rest, ok := strings.Cut(text, " "); ok {
		for _, layout := range []string{"3:04pm", "3pm"} {
			t, err := time.Parse(layout, strings.ToLower(rest))
			if err != nil {
				continue
			}
			for wd := range 7 {
				if strings.EqualFold(day, time.Weekday(wd).String()[:3]) {
					r := at(t, now.Year(), now.Month(), now.Day())
					r = r.AddDate(0, 0, (wd-int(now.Weekday())+7)%7)
					if !r.After(now) {
						r = r.AddDate(0, 0, 7)
					}
					return r
				}
			}
		}
	}
	for _, layout := range []string{"Jan 2 3:04pm", "Jan 2 3pm"} {
		if t, err := time.Parse(layout, text); err == nil {
			r := at(t, now.Year(), t.Month(), t.Day())
			if !r.After(now) {
				r = r.AddDate(1, 0, 0)
			}
			return r
		}
	}
	return time.Time{}
}

// String says when the limit resets, for reasons.
func (e *PlanLimitError) String() string {
	if e.ResetsAt.IsZero() {
		return fmt.Sprintf("the Claude plan's %s limit is reached", e.Window)
	}
	return fmt.Sprintf("the Claude plan's %s limit is reached until %s", e.Window, e.ResetsAt.Format("Mon 15:04"))
}
