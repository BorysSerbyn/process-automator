//go:build !darwin && !linux

package cli

import "time"

// schedulerLog: the scheduler doesn't run as a login item here, so it keeps no log.
var schedulerLog = func(time.Time) (string, error) { return "", nil }
