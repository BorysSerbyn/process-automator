package settings

import (
	"strings"
	"time"
)

// DurationText writes a duration the way duration settings are written: 20m,
// 12h, 1h30m.
func DurationText(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}
