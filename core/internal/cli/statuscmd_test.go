package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
)

// status --watch prints the status at once, then a line each time the file
// changes (a missing file is an empty status), and stops with its context.
func TestWatchStatus(t *testing.T) {
	orig := watchPoll
	watchPoll = 10 * time.Millisecond
	t.Cleanup(func() { watchPoll = orig })
	path := filepath.Join(t.TempDir(), "run-status.json")
	r, w := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- watchStatus(ctx, path, w); _ = w.Close() }()
	lines := bufio.NewScanner(r)
	next := func() runstatus.Status {
		t.Helper()
		if !lines.Scan() {
			t.Fatal("no line")
		}
		var s runstatus.Status
		if err := json.Unmarshal(lines.Bytes(), &s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if s := next(); s.Health != runstatus.HealthNone {
		t.Fatalf("missing file: %+v", s)
	}
	if err := runstatus.Heartbeat(path, time.Now(), time.Minute); err != nil {
		t.Fatal(err)
	}
	if s := next(); s.ValidUntil == nil {
		t.Fatalf("after a heartbeat: %+v", s)
	}
	cancel()
	go func() { _, _ = io.Copy(io.Discard, r) }()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// The MCP server never offers a tool that doesn't end.
func TestStatusWatchNotOverMCP(t *testing.T) {
	tools := toolMap(testServer(t, false))
	props := tools["status"].tool.InputSchema.(map[string]any)["properties"].(map[string]any)
	if _, ok := props["watch"]; ok {
		t.Fatal("status --watch is an MCP argument")
	}
}
