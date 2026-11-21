package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"time"
)

// RestartDelay is how long Follow waits before starting `status --watch`
// again once it ended.
var RestartDelay = 5 * time.Second

// Follow follows the core's run status through `status --watch`, which
// prints it as one JSON line at once and again each time it changes. Each
// line goes to onStatus (nil: one the tray can't read). If the command ends
// (an update replaced the core, it crashed) it's started again after
// RestartDelay; meanwhile the last status stands, and goes red once past
// its validUntil, which the caller checks on a timer. It returns when ctx
// ends.
func (c CLI) Follow(ctx context.Context, onStatus func(*RunStatus)) {
	for {
		if err := c.watch(ctx, onStatus); err != nil {
			onStatus(nil)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(RestartDelay):
		}
	}
}

// watch runs one `status --watch` until it ends; err: it didn't start.
func (c CLI) watch(ctx context.Context, onStatus func(*RunStatus)) error {
	cmd := exec.CommandContext(ctx, c.Path, "status", "--watch")
	endWithParent(cmd)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	r := bufio.NewReader(out)
	for {
		line, err := r.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 && err == nil {
			onStatus(ParseStatus(line))
		}
		if err != nil {
			if err != io.EOF {
				_, _ = io.Copy(io.Discard, r)
			}
			break
		}
	}
	_ = cmd.Wait()
	return nil
}

// ParseStatus is one line's status; unreadable, or of an unsupported
// version or health, is nil.
func ParseStatus(line []byte) *RunStatus {
	var s RunStatus
	if err := json.Unmarshal(line, &s); err != nil || s.SchemaVersion != SupportedSchemaVersion {
		return nil
	}
	switch s.Health {
	case "ok", "error", "none":
		return &s
	}
	return nil
}
