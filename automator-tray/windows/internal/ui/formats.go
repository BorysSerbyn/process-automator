package ui

import (
	"fmt"
	"math"
	"time"
)

// when is a time as automator-ui writes one: "Today at 09:20", "Yesterday
// at 17:02", "Tomorrow at 08:00", "19 Nov 2026 at 09:20" (local time).
func when(t, now time.Time) string {
	t, now = t.Local(), now.Local()
	day := func(d time.Time) time.Time { return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.Local) }
	clock := t.Format("15:04")
	switch day(t).Sub(day(now)).Round(time.Hour) / (24 * time.Hour) {
	case 0:
		return "Today at " + clock
	case -1:
		return "Yesterday at " + clock
	case 1:
		return "Tomorrow at " + clock
	}
	return t.Format("2 Jan 2006") + " at " + clock
}

// cost is dollars to the cent: "$1.25".
func cost(usd float64) string { return fmt.Sprintf("$%.2f", usd) }

// percent is a share in whole percent: "42%".
func percent(p float64) string { return fmt.Sprintf("%.0f%%", math.Round(p)) }

// number is a number as plainly as it can be written: "80", "12.5".
func number(f float64) string { return fmt.Sprintf("%g", f) }

// itoa is a whole number as text.
func itoa(n int) string { return fmt.Sprintf("%d", n) }
