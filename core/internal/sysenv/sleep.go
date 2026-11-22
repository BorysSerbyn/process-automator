package sysenv

import "time"

// SleepGap is how much more wall-clock time than running time has to pass for
// SleptSince to count it as sleep, not a clock adjustment.
const SleepGap = time.Minute

// SleptSince is how long the computer slept since t (a time.Now reading):
// the wall-clock time that passed while the monotonic clock, which stops
// while the computer sleeps on macOS and Linux, didn't. A clock adjusted in
// the meantime shows up too; under SleepGap it isn't sleep.
func SleptSince(t time.Time) time.Duration {
	now := time.Now()
	return now.Round(0).Sub(t.Round(0)) - now.Sub(t)
}

// Slept is whether the computer slept since t (a time.Now reading).
func Slept(t time.Time) bool { return SleptSince(t) >= SleepGap }
