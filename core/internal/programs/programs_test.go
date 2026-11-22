//go:build unix

package programs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// script writes an executable shell script into dir.
func script(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// source writes a program's folder (its description file and script) and
// returns the description file's path.
func source(t *testing.T, desc, body string) string {
	t.Helper()
	dir := t.TempDir()
	script(t, dir, "prog", body)
	path := filepath.Join(dir, DescriptionFile)
	if err := os.WriteFile(path, []byte(desc), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParse(t *testing.T) {
	p, err := Parse("backup", "description: Backs up\nexecutable: ./prog\nwrites: true\nschedule: \"0 9 * * *\"\n"+
		"secrets:\n  - name: api-token\n  - name: other\n    env: OTHER_KEY\n")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Writes || !p.Scheduled() || p.Secrets[0].Env != "API_TOKEN" || p.Secrets[1].Env != "OTHER_KEY" {
		t.Fatalf("%+v", p)
	}
	keys := []string{}
	for _, s := range p.Settings() {
		keys = append(keys, s.Key)
	}
	if got := strings.Join(keys, " "); got != "routines.backup.enabled routines.backup.schedule routines.backup.staleness routines.backup.timeout" {
		t.Fatalf("settings: %s", got)
	}
	for _, bad := range []string{
		"executable: ./prog\n",                       // writes must be said
		"writes: false\n",                            // no executable
		"executable: ./prog\nwrites: no\nmodel: x\n", // an unknown field
		"executable: ./prog\nwrites: false\nstaleness: 1h\n",
		"executable: ./prog\nwrites: false\nschedule: every day\n",
		"name: other\nexecutable: ./prog\nwrites: false\n",
		"executable: ./prog\nwrites: false\nsecrets:\n  - name: Bad Name\n",
		"executable: ./prog\nwrites: false\npermissions: [keyboard]\n", // an unknown permission
	} {
		if _, err := Parse("backup", bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

// A program declares the permissions it needs; they're kept sorted, each once.
func TestParsePermissions(t *testing.T) {
	p, err := Parse("snap", "executable: ./prog\nwrites: false\npermissions: [screen-recording, accessibility, screen-recording]\n")
	if err != nil || strings.Join(p.Permissions, ",") != "accessibility,screen-recording" {
		t.Fatalf("%v %v", p.Permissions, err)
	}
	if _, err := Parse("snap", "executable: ./prog\nwrites: false\npermissions: [keyboard]\n"); err == nil || !strings.Contains(err.Error(), `unknown permission "keyboard"`) {
		t.Fatalf("an unknown permission: %v", err)
	}
}

// Adding copies the description and the executable into the app's folder:
// changing the original afterwards changes nothing that runs.
func TestAddCopies(t *testing.T) {
	store := filepath.Join(t.TempDir(), "programs")
	desc := source(t, "executable: ./prog\nwrites: false\n", "echo original\n")
	p, err := Add(store, "hello", desc, false)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(p.Executable) != filepath.Join(store, "hello") {
		t.Fatalf("runs %s, not its copy", p.Executable)
	}
	before, _ := Checksum(p.Executable)
	script(t, filepath.Dir(desc), "prog", "echo swapped\n")
	if after, _ := Checksum(p.Executable); after != before {
		t.Fatal("the copy changed with the original")
	}
	if _, err := Add(store, "hello", desc, false); err == nil {
		t.Fatal("added over an existing program")
	}
	if _, err := Add(store, "other", desc, true); err == nil {
		t.Fatal("updated a program that doesn't exist")
	}
	if p, err = Add(store, "hello", desc, true); err != nil {
		t.Fatal(err)
	}
	if after, _ := Checksum(p.Executable); after == before {
		t.Fatal("an update didn't copy the new executable")
	}
	set, err := Load(store)
	if err != nil || len(set.Programs) != 1 || len(set.Errors) != 0 {
		t.Fatalf("%+v %v", set, err)
	}
	if _, err := Delete(store, "hello"); err != nil || Exists(store, "hello") {
		t.Fatalf("delete: %v", err)
	}
}

func TestAddRefusesWhatCantRun(t *testing.T) {
	store := t.TempDir()
	desc := source(t, "executable: ./prog\nwrites: false\n", "true\n")
	_ = os.Chmod(filepath.Join(filepath.Dir(desc), "prog"), 0o644)
	if _, err := Add(store, "x", desc, false); err == nil || !strings.Contains(err.Error(), "chmod") {
		t.Fatalf("added a file that can't run: %v", err)
	}
	missing := source(t, "executable: ./nothing\nwrites: false\n", "true\n")
	if _, err := Add(store, "x", missing, false); err == nil {
		t.Fatal("added a missing executable")
	}
	if Exists(store, "x") {
		t.Fatal("a failed add left a program behind")
	}
}

func run(ctx context.Context, t *testing.T, body string, args ...string) (*Execution, string, error) {
	t.Helper()
	dir := t.TempDir()
	exe := script(t, dir, "prog", body)
	var log bytes.Buffer
	res, err := Exec(ctx, Invocation{Executable: exe, Args: args, Dir: dir, Env: Environment(dir, map[string]string{"GREETING": "hi"}), Log: &log})
	return res, log.String(), err
}

func TestExecSucceeds(t *testing.T) {
	res, log, err := run(context.Background(), t, `echo "$GREETING $1 in $(pwd)"; echo "PATH=$PATH"; echo "Copied 3 files" > "$PA_RESULT"`, "there")
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || res.Summary != "Copied 3 files" || !strings.Contains(res.Output, "hi there in ") {
		t.Fatalf("%+v", res)
	}
	if !strings.Contains(log, "hi there") || !strings.Contains(res.Output, "PATH="+DefaultPath+"\n") {
		t.Fatalf("log %q, output %q", log, res.Output)
	}
}

func TestExecFails(t *testing.T) {
	res, _, err := run(context.Background(), t, "echo working; echo 'no network' >&2; exit 3\n")
	if err == nil || res.ExitCode != 3 || !strings.Contains(err.Error(), "status 3: no network") {
		t.Fatalf("%+v %v", res, err)
	}
	res, _, err = run(context.Background(), t, "echo 'quota exceeded' > \"$PA_RESULT\"; exit 1\n")
	if err == nil || !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestExecCapsOutput(t *testing.T) {
	res, log, err := run(context.Background(), t, "echo first; i=0; while [ $i -lt 3000 ]; do echo 'a line of output that takes some room'; i=$((i+1)); done; echo 'the error at the end'; exit 1\n")
	if err == nil || !strings.Contains(err.Error(), "the error at the end") {
		t.Fatalf("the failure's reason should be the output's last line: %v", err)
	}
	if !res.OutputTruncated || len(res.Output) > OutputCap+100 || len(log) > OutputCap+200 {
		t.Fatalf("kept %d, logged %d, truncated %v", len(res.Output), len(log), res.OutputTruncated)
	}
	for name, s := range map[string]string{"kept": res.Output, "logged": log} {
		if !strings.HasPrefix(s, "first\n") || !strings.Contains(s, "KiB of output left out") || !strings.HasSuffix(s, "the error at the end\n") {
			t.Errorf("%s output should keep its start and its end, with the gap marked", name)
		}
	}
	// Short output is kept whole.
	res, _, err = run(context.Background(), t, "echo short\n")
	if err != nil || res.Output != "short\n" || res.OutputTruncated {
		t.Fatalf("%+v %v", res, err)
	}
}

// Stopping, or a timeout, ends the program and everything it started: its
// whole process group.
func TestExecKillsTheGroup(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	// The child ignores SIGTERM: only the group's kill ends it.
	res, _, err := run(ctx, t, "sh -c 'trap \"\" TERM; while :; do sleep 1; done' &\necho $! > "+pidFile+"\nwait\n")
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("%+v %v", res, err)
	}
	if time.Since(start) > stopGrace+3*time.Second {
		t.Fatalf("took %s", time.Since(start))
	}
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	if _, err := fmt.Sscan(strings.TrimSpace(string(b)), &pid); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if syscall.Kill(pid, 0) != nil {
			break
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatal("its child outlived the run")
		}
	}
}

// What a program leaves running when it exits is ended with it, and doesn't
// hold the run up.
func TestExecEndsWhatItLeaves(t *testing.T) {
	start := time.Now()
	res, _, err := run(context.Background(), t, "sleep 30 &\necho started\n")
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("waited %s for what it left running", time.Since(start))
	}
}

// A program gets the desktop session's display variables the app has (to
// open an app), and nothing else from the app's environment.
func TestEnvironmentPassesTheDisplay(t *testing.T) {
	t.Setenv("DISPLAY", ":7")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("ANTHROPIC_API_KEY", "not for programs")
	env := strings.Join(Environment("/home/me", map[string]string{"TOKEN": "t"}), "\n")
	for _, want := range []string{"DISPLAY=:7", "HOME=/home/me", "TOKEN=t", "PATH=" + DefaultPath} {
		if !strings.Contains(env, want) {
			t.Errorf("lacks %s:\n%s", want, env)
		}
	}
	for _, not := range []string{"ANTHROPIC_API_KEY", "WAYLAND_DISPLAY="} {
		if strings.Contains(env, not) {
			t.Errorf("has %s:\n%s", not, env)
		}
	}
}
