package cli

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/product"
)

// schedulerLog is the scheduler's log since a time: on macOS, the file its
// LaunchAgent writes (install-app.sh names it), all of it; the caller keeps
// the lines it needs. None yet is empty. Tests replace it.
var schedulerLog = func(time.Time) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(filepath.Join(home, "Library", "Logs", product.Folder, "scheduler.log"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return string(b), err
}
