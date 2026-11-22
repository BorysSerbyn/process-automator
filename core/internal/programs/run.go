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
// kept with the run: its first half and its last half.
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
	// Log gets its output's first half as it comes, and its last half when it
	// ends (OutputCap).
	Log io.Writer
	// Stdin, if set, is a file whose content it reads on stdin (a hook's
	// event); otherwise stdin is empty.
	Stdin string
	// Stdout, if set, gets its stdout as it comes (a watcher's events), and
	// only its stderr is its output; otherwise both are.
	Stdout io.Writer
}

// Execution is how a program's run went: the program's section of its run's
// record.
type Execution struct {
	// ExitCode is the code it exited with (-1 when it was killed).
	ExitCode int `json:"exitCode"`
	// Summary is the line it wrote to $PA_RESULT, if it wrote one.
	Summary string `json:"summary,omitempty"`
	// Output is what it printed (stdout and stderr): all of it, or its first
	// and last 32 KiB with what was left out marked between them.
	Output string `json:"output,omitempty"`
	// OutputTruncated is true when it printed more than Output holds.
	OutputTruncated bool `json:"outputTruncated,omitempty"`
	DryRun          bool `json:"dryRun"`
	// Checksum is the SHA-256 of the executable that ran.
	Checksum string `json:"checksum,omitempty"`
}

// DisplayEnv are the desktop session's variables a program is given when the
// app has them, so it can open an app or a window (on Linux, the display and
// the session bus; macOS needs none).
var DisplayEnv = []string{"DISPLAY", "WAYLAND_DISPLAY", "XAUTHORITY", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS", "XDG_SESSION_TYPE", "XDG_CURRENT_DESKTOP"}

// Environment is what a program runs with, and nothing else from the app's
// own: a fixed PATH, HOME, TMPDIR, the desktop session's DisplayEnv that are
// set, and vars (secrets, what the run is for).
func Environment(home string, vars map[string]string) []string {
	env := []string{"PATH=" + DefaultPath, "HOME=" + home, "TMPDIR=" + os.TempDir()}
	for _, k := range DisplayEnv {
		if v, ok := os.LookupEnv(k); ok && v != "" {
			env = append(env, k+"="+v)
		}
	}
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
	// Its stdout, when it's read apart: through a pipe of our own too.
	var outR, outW *os.File
	stdoutDone := make(chan struct{})
	close(stdoutDone)
	if inv.Stdout != nil {
		if outR, outW, err = os.Pipe(); err != nil {
			_ = r.Close()
			_ = w.Close()
			<-copied
			return nil, err
		}
		cmd.Stdout = outW
		stdoutDone = make(chan struct{})
		go func() {
			defer close(stdoutDone)
			_, _ = io.Copy(inv.Stdout, outR)
		}()
	}
	if inv.Stdin != "" {
		in, err := os.Open(inv.Stdin)
		if err != nil {
			_ = r.Close()
			_ = w.Close()
			<-copied
			return nil, err
		}
		defer func() { _ = in.Close() }()
		cmd.Stdin = in
	}
	newGroup(cmd)
	startErr := cmd.Start()
	_ = w.Close()
	if outW != nil {
		_ = outW.Close()
	}
	if startErr != nil {
		_ = r.Close()
		<-copied
		if outR != nil {
			_ = outR.Close()
			<-stdoutDone
		}
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
	if outR != nil {
		select {
		case <-stdoutDone:
		case <-time.After(time.Second):
		}
		_ = outR.Close()
		<-stdoutDone
	}
	out.finish()

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

// capped keeps a run's output within limit: the first half as it comes
// (logged live), and the last half, logged when the run ends. A failing
// program's error is at the end, so the end is never lost.
type capped struct {
	mu      sync.Mutex
	log     io.Writer
	limit   int
	head    bytes.Buffer
	tail    []byte
	skipped int64
}

func (c *capped) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(p)
	half := c.limit / 2
	if room := half - c.head.Len(); room > 0 {
		take := min(room, len(p))
		c.head.Write(p[:take])
		if c.log != nil {
			_, _ = c.log.Write(p[:take])
		}
		p = p[take:]
	}
	if len(p) == 0 {
		return n, nil
	}
	if c.tail == nil && c.log != nil {
		fmt.Fprintf(c.log, "\n[output past %d KiB: its last %d KiB follow when it ends]\n", half>>10, half>>10)
	}
	c.tail = append(c.tail, p...)
	if over := len(c.tail) - half; over > 0 {
		c.skipped += int64(over)
		copy(c.tail, c.tail[over:])
		c.tail = c.tail[:half]
	}
	return n, nil
}

// gap marks the output left out between the head and the tail, if any.
func (c *capped) gap() string {
	if c.skipped == 0 {
		return ""
	}
	return fmt.Sprintf("\n[… %d KiB of output left out …]\n", (c.skipped+1023)>>10)
}

// finish logs the tail, once the program's output has ended.
func (c *capped) finish() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tail != nil && c.log != nil {
		_, _ = io.WriteString(c.log, c.gap())
		_, _ = c.log.Write(c.tail)
	}
}

// text is the output kept: all of it, or its head and tail with the gap
// marked; and whether anything was left out.
func (c *capped) text() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tail == nil {
		return c.head.String(), false
	}
	return c.head.String() + c.gap() + string(c.tail), c.skipped > 0
}
