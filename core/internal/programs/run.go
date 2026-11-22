package programs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// What a program is told when it runs, as environment variables.
const (
	// ResultEnv is the file a program may write a one-line summary of its
	// run to (what it did, or why it failed).
	ResultEnv = "PA_RESULT"
	// TriggerEnv is what started the run: schedule, manual or backfill.
	TriggerEnv = "PA_TRIGGER"
	// ForEnv is the time the run is for (RFC 3339): its schedule slot, the
	// backfilled day, or now.
	ForEnv = "PA_FOR"
	// RunIDEnv is the run's ID in the run history.
	RunIDEnv = "PA_RUN_ID"
)

// DryRunFlag is passed to a program that writes, on a dry run.
const DryRunFlag = "--dry-run"

// OutputCap is how much of a run's output (stdout and stderr) is logged and
// kept with the run.
const OutputCap = 64 << 10

// stopGrace is how long a program has to end after it's asked to (stop,
// timeout, shutdown) before its process group is killed.
const stopGrace = 5 * time.Second

// Invocation is one run of a program.
type Invocation struct {
	Executable string
	Args       []string
	// Dir is the folder it runs in.
	Dir string
	// Env is its whole environment (Environment's), but ResultEnv.
	Env []string
	// Log gets its output as it comes, up to OutputCap.
	Log io.Writer
}

// Execution is how a program's run went: the program's section of its run's
// record.
type Execution struct {
	// ExitCode is the code it exited with (-1 when it was killed).
	ExitCode int `json:"exitCode"`
	// Summary is the line it wrote to $PA_RESULT, if it wrote one.
	Summary string `json:"summary,omitempty"`
	// Output is what it printed (stdout and stderr), up to the first 64 KiB.
	Output string `json:"output,omitempty"`
	// OutputTruncated is true when it printed more than Output holds.
	OutputTruncated bool `json:"outputTruncated,omitempty"`
	DryRun          bool `json:"dryRun"`
	// Checksum is the SHA-256 of the executable that ran.
	Checksum string `json:"checksum,omitempty"`
}

// Environment is what a program runs with, and nothing else from the app's
// own: a fixed PATH, HOME, TMPDIR, and vars (secrets, what the run is for).
func Environment(home string, vars map[string]string) []string {
	env := []string{"PATH=" + DefaultPath, "HOME=" + home, "TMPDIR=" + os.TempDir()}
	for k, v := range vars {
		env = append(env, k+"="+v)
	}
	return env
}

// Exec runs a program in a process group of its own and waits for it: exit
// code 0 is success. When ctx ends (stop, timeout, shutdown) the group is
// asked to end, then killed; whatever it leaves running when it exits is
// killed too.
func Exec(ctx context.Context, inv Invocation) (*Execution, error) {
	tmp, err := os.MkdirTemp("", "program-run-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	resultPath := filepath.Join(tmp, "result")

	// The output goes through a pipe of our own, so the run ends when the
	// program exits, not when the last process holding its output does.
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	out := &capped{log: inv.Log, limit: OutputCap}
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		_, _ = io.Copy(out, r)
	}()
	cmd := exec.Command(inv.Executable, inv.Args...)
	cmd.Dir = inv.Dir
	cmd.Env = append(slices.Clone(inv.Env), ResultEnv+"="+resultPath)
	cmd.Stdout, cmd.Stderr = w, w
	newGroup(cmd)
	startErr := cmd.Start()
	_ = w.Close()
	if startErr != nil {
		_ = r.Close()
		<-copied
		return nil, fmt.Errorf("couldn't start it: %w", startErr)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	var waitErr error
	stopped := false
	select {
	case waitErr = <-exited:
	case <-ctx.Done():
		stopped = true
		signalGroup(cmd, false)
		select {
		case waitErr = <-exited:
		case <-time.After(stopGrace):
			signalGroup(cmd, true)
			waitErr = <-exited
		}
	}
	signalGroup(cmd, true) // anything it left behind
	select {
	case <-copied:
	case <-time.After(time.Second): // a process that left its group holds the output
	}
	_ = r.Close()

	res := &Execution{ExitCode: cmd.ProcessState.ExitCode(), Summary: summary(resultPath)}
	res.Output, res.OutputTruncated = out.text()
	if stopped {
		return res, fmt.Errorf("it was stopped: %w", context.Cause(ctx))
	}
	var exit *exec.ExitError
	switch {
	case waitErr == nil:
		return res, nil
	case errors.As(waitErr, &exit) && res.ExitCode >= 0:
		why := res.Summary
		if why == "" {
			why = lastLine(res.Output)
		}
		if why == "" {
			return res, fmt.Errorf("it exited with status %d", res.ExitCode)
		}
		return res, fmt.Errorf("it exited with status %d: %s", res.ExitCode, why)
	default:
		return res, fmt.Errorf("it ended abnormally: %w", waitErr)
	}
}

// summaryCap is how much of $PA_RESULT is read: a line is enough.
const summaryCap = 500

// summary is the first line written to the result file, if any.
func summary(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	b, _ := io.ReadAll(io.LimitReader(f, 4<<10))
	for _, line := range strings.Split(string(b), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			if r := []rune(line); len(r) > summaryCap {
				line = string(r[:summaryCap-1]) + "…"
			}
			return line
		}
	}
	return ""
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	line := strings.TrimSpace(lines[len(lines)-1])
	if r := []rune(line); len(r) > summaryCap {
		line = string(r[:summaryCap-1]) + "…"
	}
	return line
}

// capped keeps and logs the first limit bytes written to it, and says so
// once when there were more.
type capped struct {
	mu        sync.Mutex
	log       io.Writer
	limit     int
	buf       bytes.Buffer
	truncated bool
}

func (c *capped) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(p)
	if room := c.limit - c.buf.Len(); len(p) > room {
		p = p[:max(room, 0)]
		if !c.truncated && c.log != nil {
			defer fmt.Fprintf(c.log, "\n[output past %d KiB isn't logged or kept]\n", c.limit>>10)
		}
		c.truncated = true
	}
	c.buf.Write(p)
	if c.log != nil && len(p) > 0 {
		_, _ = c.log.Write(p)
	}
	return n, nil
}

func (c *capped) text() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String(), c.truncated
}
