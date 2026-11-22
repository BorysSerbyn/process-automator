package engine

import "time"

// FakeSleep makes every run look like the computer slept during it, for the test.
func FakeSleep(t interface{ Cleanup(func()) }) {
	orig := slept
	slept = func(time.Time) bool { return true }
	t.Cleanup(func() { slept = orig })
}
