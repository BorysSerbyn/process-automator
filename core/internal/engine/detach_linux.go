package engine

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/product"
)

// startDetached starts cmd so it outlives this process and whatever stops
// it. Inside a systemd service (the scheduler's unit) a new session isn't
// enough: restarting the unit kills everything in its cgroup, the update
// that restarts it included, so the command runs as a transient unit of
// its own (systemd-run), its output appended to logPath. Elsewhere (a
// terminal, the tray) it starts in a session of its own.
func startDetached(cmd *exec.Cmd, logPath string) error {
	if os.Getenv("INVOCATION_ID") != "" {
		if run, err := exec.LookPath("systemd-run"); err == nil {
			args := []string{"--user", "--collect", "--quiet",
				"--unit=" + product.Command + "-update-" + strconv.FormatInt(time.Now().Unix(), 10),
				"--property=StandardOutput=append:" + logPath, "--property=StandardError=append:" + logPath}
			for _, kv := range cmd.Env {
				name, _, _ := strings.Cut(kv, "=")
				if name == DetachedUpdateEnv || name == AutoUpdateEnv || name == "PATH" {
					args = append(args, "--setenv="+kv)
				}
			}
			args = append(append(args, "--"), cmd.Args...)
			unit := exec.Command(run, args...)
			unit.Stdout, unit.Stderr = cmd.Stdout, cmd.Stderr
			return unit.Run()
		}
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
