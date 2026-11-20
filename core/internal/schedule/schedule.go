// Package schedule is what frontends need to show and edit routine schedules
// without knowing cron: a description in plain words, the next times, and a
// simple picker (days of the week and a time) that converts to cron and back.
package schedule

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	desc "github.com/lnquy/cron"
	"github.com/robfig/cron/v3"
)

// Days are the picker's day names, in cron's order (0 = Sunday).
var Days = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// Picker is a schedule as days of the week and a time of day.
type Picker struct {
	// Days the routine runs, from Days (all seven: every day).
	Days []string `json:"days" jsonschema:"enum=sun,enum=mon,enum=tue,enum=wed,enum=thu,enum=fri,enum=sat"`
	// At is the time, HH:MM (24-hour).
	At string `json:"at"`
}

// Info is a schedule as frontends show it.
type Info struct {
	Cron string `json:"cron"`
	// Text is the schedule in plain words ("At 09:20, Monday through Friday").
	Text string `json:"text"`
	// Picker is the schedule as days and a time, when it is one (nil: only
	// cron can express it).
	Picker *Picker `json:"picker,omitempty"`
	// Next are the next few times it runs.
	Next []time.Time `json:"next"`
}

// Describe says what a cron expression means, with its next n times after now.
func Describe(expr string, now time.Time, n int) (Info, error) {
	sched, err := cron.ParseStandard(expr)
	if err != nil {
		return Info{}, fmt.Errorf("not a valid schedule %q: %w", expr, err)
	}
	info := Info{Cron: expr, Text: Text(expr), Picker: FromCron(expr), Next: []time.Time{}}
	t := now
	for range n {
		t = sched.Next(t)
		info.Next = append(info.Next, t)
	}
	return info, nil
}

// Text is a cron expression in plain words, or the expression itself when it
// can't be described.
func Text(expr string) string {
	d, err := desc.NewDescriptor(desc.Use24HourTimeFormat(true))
	if err != nil {
		return expr
	}
	s, err := d.ToDescription(expr, desc.Locale_en)
	if err != nil {
		return expr
	}
	return s
}

// Build is the cron expression for a picker's days and time.
func Build(p Picker) (string, error) {
	t, err := time.Parse("15:04", p.At)
	if err != nil {
		return "", fmt.Errorf("the time %q isn't HH:MM", p.At)
	}
	if len(p.Days) == 0 {
		return "", errors.New("pick at least one day")
	}
	var nums []int
	for _, d := range p.Days {
		i := slices.Index(Days, strings.ToLower(d))
		if i < 0 {
			return "", fmt.Errorf("unknown day %q: one of %s", d, strings.Join(Days, ", "))
		}
		if !slices.Contains(nums, i) {
			nums = append(nums, i)
		}
	}
	slices.Sort(nums)
	return fmt.Sprintf("%d %d * * %s", t.Minute(), t.Hour(), dayField(nums)), nil
}

// dayField writes days as cron does by hand: * for all, ranges where they run on.
func dayField(nums []int) string {
	if len(nums) == 7 {
		return "*"
	}
	var parts []string
	for i := 0; i < len(nums); {
		j := i
		for j+1 < len(nums) && nums[j+1] == nums[j]+1 {
			j++
		}
		if j-i >= 2 {
			parts = append(parts, fmt.Sprintf("%d-%d", nums[i], nums[j]))
		} else {
			for k := i; k <= j; k++ {
				parts = append(parts, strconv.Itoa(nums[k]))
			}
		}
		i = j + 1
	}
	return strings.Join(parts, ",")
}

// FromCron reads a cron expression as a picker, when it's one: a single
// minute and hour, any day of the month and month, and days of the week as
// *, numbers or ranges. Otherwise nil.
func FromCron(expr string) *Picker {
	f := strings.Fields(expr)
	if len(f) != 5 || f[2] != "*" || f[3] != "*" {
		return nil
	}
	minute, err1 := strconv.Atoi(f[0])
	hour, err2 := strconv.Atoi(f[1])
	if err1 != nil || err2 != nil || minute < 0 || minute > 59 || hour < 0 || hour > 23 {
		return nil
	}
	days, ok := parseDays(f[4])
	if !ok {
		return nil
	}
	return &Picker{Days: days, At: fmt.Sprintf("%02d:%02d", hour, minute)}
}

func parseDays(field string) ([]string, bool) {
	if field == "*" {
		return slices.Clone(Days), true
	}
	seen := map[int]bool{}
	for _, part := range strings.Split(field, ",") {
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.Atoi(lo)
		if err != nil {
			return nil, false
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(hi); err != nil {
				return nil, false
			}
		}
		if a < 0 || b > 7 || a > b {
			return nil, false
		}
		for d := a; d <= b; d++ {
			seen[d%7] = true // 7 is Sunday too
		}
	}
	var days []string
	for i, name := range Days {
		if seen[i] {
			days = append(days, name)
		}
	}
	return days, len(days) > 0
}
