// Package cli is the core's CLI, the tray's only way to ask the core
// anything or have it do something: it runs the app's commands and reads
// what they print, as automator-ui's CLI does. Every call blocks until the
// command exits: make them off the tray's loop.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Timeout bounds every command Exec runs: one that hangs (a keyring prompt
// no one answers, a network that never replies) mustn't hold up what waits
// for it (notifications, update checks) for good.
var Timeout = 2 * time.Minute

// CLI runs the app's CLI at Path.
type CLI struct {
	Path string
	// Command is the CLI's name, for messages.
	Command string
}

// Failure is why a command failed, in the core's words.
type Failure struct{ Message string }

func (f *Failure) Error() string { return f.Message }

// Exec runs a command and returns what it printed. err is a *Failure: the
// core's error object (commands given --json print one), else what it wrote
// to stderr.
func (c CLI) Exec(args ...string) ([]byte, error) {
	return c.exec(Timeout, args)
}

// exec runs a command, stopping it after limit (0: no limit, for a command
// that waits for me, as signing in does).
func (c CLI) exec(limit time.Duration, args []string) ([]byte, error) {
	ctx, cancel := context.Background(), context.CancelFunc(func() {})
	if limit > 0 {
		ctx, cancel = context.WithTimeout(ctx, limit)
	}
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Path, args...)
	// What it started that keeps its output open isn't waited for long.
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, &Failure{Message: fmt.Sprintf("%s %s didn't answer in %s", c.Command, strings.Join(args, " "), limit)}
		}
		if _, ok := err.(*exec.ExitError); !ok {
			return nil, &Failure{Message: "couldn't start " + c.Command}
		}
		return stdout.Bytes(), c.failure(stdout.Bytes(), stderr.String())
	}
	return stdout.Bytes(), nil
}

func (c CLI) failure(stdout []byte, stderr string) *Failure {
	var out errorOutput
	if json.Unmarshal(stdout, &out) == nil && out.Error != "" {
		return &Failure{Message: out.Error}
	}
	msg := strings.TrimPrefix(strings.TrimSpace(stderr), "Error: ")
	if msg == "" {
		msg = c.Command + " failed"
	}
	return &Failure{Message: msg}
}

// call runs a command given --json and decodes what it printed into out.
func (c CLI) call(out any, args ...string) error {
	stdout, err := c.Exec(append(args, "--json")...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(stdout, out); err != nil {
		return &Failure{Message: fmt.Sprintf("%s answered something unexpected: %v", c.Command, err)}
	}
	return nil
}

// PendingNotifications are the app's own notifications no frontend has
// delivered yet, oldest first (`notifications list --pending`).
func (c CLI) PendingNotifications() ([]Notification, error) {
	var out []Notification
	return out, c.call(&out, "notifications", "list", "--pending")
}

// MarkDelivered records that notifications were shown.
func (c CLI) MarkDelivered(ids []string) error {
	_, err := c.Exec(append([]string{"notifications", "delivered"}, ids...)...)
	return err
}

// OpenNotification says what clicking a notification shows (`notifications open`).
func (c CLI) OpenNotification(id string) (ClickOutput, error) {
	var out ClickOutput
	return out, c.call(&out, "notifications", "open", id)
}

// OpenRun opens a readable report of a run (the core writes and opens it).
func (c CLI) OpenRun(id string) error {
	_, err := c.Exec("runs", "show", id, "--open")
	return err
}

// UpdateCheck says whether a newer release exists, and whether one is
// installing (`update --check`).
func (c CLI) UpdateCheck() (UpdateOutput, error) {
	var out UpdateOutput
	return out, c.call(&out, "update", "--check")
}

// StartUpdate starts installing the newer release in the background
// (`update --detach`); the app restarts when it's done, and the result comes
// as a notification.
func (c CLI) StartUpdate() (UpdateOutput, error) {
	var out UpdateOutput
	return out, c.call(&out, "update", "--detach")
}

// UpdateProgress is what the update installing is doing, or how the latest
// one ended (`update progress`).
func (c CLI) UpdateProgress() (UpdateProgress, error) {
	var out UpdateProgress
	return out, c.call(&out, "update", "progress")
}

// Status is the run status now (`status`), for a decision made once (which
// window opening the app shows); the tray follows it with Follow.
func (c CLI) Status() (RunStatus, error) {
	var out RunStatus
	return out, c.call(&out, "status")
}

// StartScheduler starts the scheduler if it isn't running (`login-items
// start`): opening the app does.
func (c CLI) StartScheduler() (RunningOutput, error) {
	var out RunningOutput
	return out, c.call(&out, "login-items", "start")
}

// StopApp stops the scheduler, and any other frontend running (`login-items
// stop`): Quit does, then quits. It waits for the scheduler to stop (its
// runs end first): no time limit beyond the command's own.
func (c CLI) StopApp() (RunningOutput, error) {
	var out RunningOutput
	return out, c.call(&out, "login-items", "stop")
}

// Paths is where the app keeps its files (`paths`): the tray reads only
// where its log goes.
func (c CLI) Paths() (Paths, error) {
	var out Paths
	return out, c.call(&out, "paths")
}
