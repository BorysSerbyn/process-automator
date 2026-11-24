package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/atomicfile"
)

// An agent CLI's protocol (ClaudeCodeProtocol, CodexProtocol) has an
// implementation per CLI version that changed something the backend relies
// on, each tagged with the oldest version it's for (Since). The installed
// CLI's version (InstalledVersion) picks the newest one at or below it
// (protocolFor); one older than every implementation is refused (TooOldError).

// Version is a dotted version, like 2.1.331.
type Version []int

var versionRE = regexp.MustCompile(`\d+(\.\d+)+`)

// ParseVersion reads the first dotted version in s (`claude --version`
// prints "2.1.331 (Claude Code)", `codex --version` "codex-cli 0.171.0").
func ParseVersion(s string) (Version, error) {
	m := versionRE.FindString(s)
	if m == "" {
		return nil, fmt.Errorf("no version in %q", strings.TrimSpace(s))
	}
	var v Version
	for _, part := range strings.Split(m, ".") {
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, err
		}
		v = append(v, n)
	}
	return v, nil
}

// mustVersion is for the versions implementations are tagged with.
func mustVersion(s string) Version {
	v, err := ParseVersion(s)
	if err != nil {
		panic(err)
	}
	return v
}

// Less says v is older than w (missing parts count as 0).
func (v Version) Less(w Version) bool {
	for i := 0; i < len(v) || i < len(w); i++ {
		a, b := 0, 0
		if i < len(v) {
			a = v[i]
		}
		if i < len(w) {
			b = w[i]
		}
		if a != b {
			return a < b
		}
	}
	return false
}

func (v Version) String() string {
	parts := make([]string, len(v))
	for i, n := range v {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ".")
}

// versioned is an implementation of an agent CLI's protocol.
type versioned interface{ Since() Version }

// protocolFor is the newest of impls (oldest first) whose Since is v or
// older; ok is false when v is older than every one.
func protocolFor[P versioned](impls []P, v Version) (p P, ok bool) {
	for i := len(impls) - 1; i >= 0; i-- {
		if !v.Less(impls[i].Since()) {
			return impls[i], true
		}
	}
	return p, false
}

// TooOldError is an agent CLI older than every implementation of its protocol.
type TooOldError struct {
	CLI        string // its name, as I know it ("Claude Code", "Codex")
	Have, Need Version
	Update     string // how to update it
}

func (e *TooOldError) Error() string {
	return fmt.Sprintf("%s %s is too old: this app needs %s or later (update it with %s)", e.CLI, e.Have, e.Need, e.Update)
}

// An agent CLI's version is kept, by binary, in memory and in a file (the
// cacheFile InstalledVersion is given) so short-lived commands share it:
// the scheduler asks on every pass, CLI commands and the setup window on
// each run. It's asked again when the binary changes (an update replaces
// it), after an hour (a version manager's shim doesn't change when what it
// runs does), and a failure is kept for a minute (a CLI that hangs on
// --version mustn't hold up every pass).
const (
	versionTTL      = time.Hour
	versionErrorTTL = time.Minute
)

type cachedVersion struct {
	Stamp   string    `json:"stamp"`
	Version string    `json:"version,omitempty"`
	Error   string    `json:"error,omitempty"`
	At      time.Time `json:"at"`
}

func (c cachedVersion) fresh(stamp string, now time.Time) bool {
	ttl := versionTTL
	if c.Error != "" {
		ttl = versionErrorTTL
	}
	return c.Stamp == stamp && !c.At.After(now) && now.Sub(c.At) < ttl
}

func (c cachedVersion) result() (Version, error) {
	if c.Error != "" {
		return nil, errors.New(c.Error)
	}
	return ParseVersion(c.Version)
}

var cliVersions = struct {
	sync.Mutex
	m map[string]cachedVersion
}{m: map[string]cachedVersion{}}

func forgetVersions() {
	cliVersions.Lock()
	cliVersions.m = map[string]cachedVersion{}
	cliVersions.Unlock()
}

// runVersion runs `<path> --version`, with nothing on its standard input.
// Tests replace it (SetVersionForTests): they never run an agent CLI.
var runVersion = func(ctx context.Context, path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, path, "--version").Output() // stdin: /dev/null
}

// SetVersionForTests makes every agent CLI report version(name) (name is
// its binary's, e.g. "claude" or "codex"), or fail with its error, without
// running it; it returns the function that undoes it.
func SetVersionForTests(version func(name string) (string, error)) (undo func()) {
	saved := runVersion
	runVersion = func(_ context.Context, path string) ([]byte, error) {
		v, err := version(strings.TrimSuffix(filepath.Base(path), ".exe"))
		return []byte(v), err
	}
	forgetVersions()
	return func() {
		runVersion = saved
		forgetVersions()
	}
}

// InstalledVersion is the version of the agent CLI at path, kept in
// cacheFile ("" keeps it in memory only).
func InstalledVersion(ctx context.Context, path, cacheFile string) (Version, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	stamp := fmt.Sprint(info.ModTime().UnixNano(), info.Size())
	now := time.Now()
	cliVersions.Lock()
	c, ok := cliVersions.m[path]
	cliVersions.Unlock()
	if !ok && cacheFile != "" {
		c, ok = readVersionCache(cacheFile)[path]
	}
	if ok && c.fresh(stamp, now) {
		return c.result()
	}
	c = cachedVersion{Stamp: stamp, At: now}
	out, err := runVersion(ctx, path)
	var v Version
	if err == nil {
		v, err = ParseVersion(string(out))
	}
	if err != nil {
		err = fmt.Errorf("%s --version: %w", filepath.Base(path), err)
		if ctx.Err() != nil {
			return nil, err // cancelled: not the binary's answer
		}
		c.Error = err.Error()
	} else {
		c.Version = v.String()
	}
	cliVersions.Lock()
	cliVersions.m[path] = c
	cliVersions.Unlock()
	if cacheFile != "" {
		writeVersionCache(cacheFile, path, c)
	}
	return v, err
}

func readVersionCache(file string) map[string]cachedVersion {
	m := map[string]cachedVersion{}
	if b, err := os.ReadFile(file); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

// writeVersionCache records path's entry; a failure only costs a --version
// next time.
func writeVersionCache(file, path string, c cachedVersion) {
	_ = atomicfile.UpdateJSON(file, func(p string) (map[string]cachedVersion, error) {
		return readVersionCache(p), nil
	}, func(m map[string]cachedVersion) error {
		m[path] = c
		return nil
	})
}
