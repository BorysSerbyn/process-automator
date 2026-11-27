package cli

import (
	"os/exec"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/loginitems"
)

// schedulerLog is the scheduler's log since a time: on Linux, its systemd
// user unit's journal (its messages carry their own times). Tests replace it.
var schedulerLog = func(since time.Time) (string, error) {
	out, err := exec.Command("journalctl", "--user", "-u", loginitems.Labels()["scheduler"],
		"--since", since.Format("2006-01-02 15:04:05"), "--no-pager", "-o", "cat").Output()
	return string(out), err
}
