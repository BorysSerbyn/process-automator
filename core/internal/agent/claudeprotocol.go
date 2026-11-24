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
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/atomicfile"
	"github.com/BorysSerbyn/process-automator/core/internal/statusline"
	"github.com/BorysSerbyn/process-automator/core/internal/usage"
)

// ClaudeCodeProtocol is everything the claude-code backend asks of Claude
// Code: the command lines it runs and how it reads what they print. Each
// implementation is for the Claude Code versions from Since() up to the next
// implementation's, so a Claude Code release that changes something the
// backend relies on gets a new implementation and the older ones stay.
//
// The newest implementation is what a new install requires (install.sh's
// CLAUDE_CODE_MIN, which a test keeps equal to it). An app already installed
// uses the newest one at or below its Claude Code's version, so updating the
// app never makes me update Claude Code, until it's older than every
// implementation kept.
type ClaudeCodeProtocol interface {
	// Since is the oldest Claude Code version this implementation is for.
	Since() ClaudeCodeVersion
	// RunArgs is a headless run's command line, after the binary.
	RunArgs(r ClaudeRun) []string
	// RunEnv is a run's environment: base (the caller's), minus anything
	// that would bill an API key, plus the OAuth token when there's one.
	RunEnv(base []string, oauthToken string) []string
	// Result reads a run's standard output.
	Result(stdout []byte) (ClaudeResult, error)
	// UsageArgs is the command line that prints the plan's usage report.
	UsageArgs() []string
	// UsageReport reads the plan's limits from UsageArgs' output.
	UsageReport(stdout []byte) ([]usage.Limit, error)
	// MCPAddArgs registers a stdio MCP server at user scope.
	MCPAddArgs(name string, command []string) []string
	// MCPRemoveArgs removes the user-scope MCP server name.
	MCPRemoveArgs(name string) []string
	// MCPServer is the user-scope MCP server registered as name, read from
	// Claude Code's own config (no claude process, so it's quick enough for
	// every scheduler pass); ok is false when there's none.
	MCPServer(name string) (s ClaudeMCPServer, ok bool, err error)
	// ProjectInstructions is the file in a folder Claude Code takes project
	// instructions from (the core adds it to the system prompt itself).
	ProjectInstructions() string
	// Builtins are Claude Code's built-in tools as routines name them.
	Builtins() *BuiltinSet
	// PlanLimit reads a run's message as the plan's usage limit being hit,
	// nil for any other message.
	PlanLimit(msg string, now time.Time) *PlanLimitError
	// SignInHint is what to add to a run's error message when it's Claude
	// Code not signed in (how to sign in), "" for any other.
	SignInHint(msg string) string
	// StatusLine is where Claude Code's settings keep its status line.
	StatusLine() (statusline.Format, error)
	// StatusLineUsage reads the plan's 5-hour (session) and weekly windows
	// from the status line's input; nil for one it doesn't carry.
	StatusLineUsage(input []byte) (session, weekly *usage.PlanWindow, err error)
}

// ClaudeStatusLineFormats are the status line formats of every implementation
// kept, newest first, each once: where to look for the app's status line
// when the installed Claude Code can't say (it's gone, or too old).
func ClaudeStatusLineFormats() []statusline.Format {
	var out []statusline.Format
	for i := len(claudeCodeProtocols) - 1; i >= 0; i-- {
		f, err := claudeCodeProtocols[i].StatusLine()
		if err == nil && !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out
}

// ClaudeCodeForStatusLine is the implementation for the Claude Code that ran
// the status line, from the version its input carries (no claude process:
// the status line runs often); the newest when it carries none.
func ClaudeCodeForStatusLine(input []byte) (ClaudeCodeProtocol, error) {
	var in struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(input, &in) != nil || in.Version == "" {
		return NewestClaudeCode(), nil
	}
	v, err := ParseClaudeCodeVersion(in.Version)
	if err != nil {
		return NewestClaudeCode(), nil
	}
	return ClaudeCodeFor(v)
}

// ClaudeMCPServer is a stdio MCP server as Claude Code runs it.
type ClaudeMCPServer struct {
	Command string
	Args    []string
}

// ClaudeMCPProblem says why Claude Code (through p) doesn't have the app's
// MCP server, registered as name, "" when it has: none is registered, or one
// whose command is gone.
func ClaudeMCPProblem(p ClaudeCodeProtocol, name string) (string, error) {
	s, ok, err := p.MCPServer(name)
	switch {
	case err != nil:
		return "", err
	case !ok:
		return "Claude Code doesn't have " + name + "'s MCP server, so /mcp doesn't list it", nil
	case s.Command != "" && filepath.IsAbs(s.Command) && !isFile(s.Command):
		return "Claude Code's " + name + " MCP server runs " + s.Command + ", which isn't there anymore", nil
	}
	return "", nil
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// ClaudeRun is a headless run as the backend has worked it out; a protocol
// turns it into Claude Code's flags.
// The user prompt isn't here: it's given on standard input.
type ClaudeRun struct {
	System      string
	Builtins    []string // the built-in tools allowed; none is no built-ins
	MCP         MCPEndpoint
	ToolTimeout time.Duration // the longest one call to the app's tools may take
	MaxTurns    int
	Schema      string // the output's JSON schema, if any
	SessionID   string // the session to save under; "" saves none
	Model       string // "" is Claude Code's default
	Effort      string
}

// ClaudeResult is what a run printed.
type ClaudeResult struct {
	Text       string
	Structured any
	IsError    bool
	Subtype    string
	// Tokens: input counts cached and cache-creating input too.
	InputTokens, OutputTokens, CachedTokens int
}

// claudeCodeProtocols are the implementations kept, oldest first.
var claudeCodeProtocols = []ClaudeCodeProtocol{claudeCode2_1_331{}}

// NewestClaudeCode is the newest implementation: what a new install requires.
func NewestClaudeCode() ClaudeCodeProtocol { return claudeCodeProtocols[len(claudeCodeProtocols)-1] }

// OldestClaudeCode is the oldest implementation: older Claude Codes don't run.
func OldestClaudeCode() ClaudeCodeProtocol { return claudeCodeProtocols[0] }

// ClaudeCodeFor is the implementation for Claude Code version v: the newest
// whose Since is v or older.
func ClaudeCodeFor(v ClaudeCodeVersion) (ClaudeCodeProtocol, error) {
	for i := len(claudeCodeProtocols) - 1; i >= 0; i-- {
		if p := claudeCodeProtocols[i]; !v.Less(p.Since()) {
			return p, nil
		}
	}
	return nil, &ClaudeCodeTooOld{Have: v, Need: OldestClaudeCode().Since()}
}

// ClaudeCodeTooOld is a Claude Code older than every implementation.
type ClaudeCodeTooOld struct{ Have, Need ClaudeCodeVersion }

func (e *ClaudeCodeTooOld) Error() string {
	return fmt.Sprintf("Claude Code %s is too old: this app needs %s or later (update it with `claude update`)", e.Have, e.Need)
}

// ClaudeCodeVersion is a dotted version, like 2.1.331.
type ClaudeCodeVersion []int

var versionRE = regexp.MustCompile(`\d+(\.\d+)+`)

// ParseClaudeCodeVersion reads the first dotted version in s (`claude
// --version` prints "2.1.331 (Claude Code)").
func ParseClaudeCodeVersion(s string) (ClaudeCodeVersion, error) {
	m := versionRE.FindString(s)
	if m == "" {
		return nil, fmt.Errorf("no version in %q", strings.TrimSpace(s))
	}
	var v ClaudeCodeVersion
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
func mustVersion(s string) ClaudeCodeVersion {
	v, err := ParseClaudeCodeVersion(s)
	if err != nil {
		panic(err)
	}
	return v
}

// Less says v is older than w (missing parts count as 0).
func (v ClaudeCodeVersion) Less(w ClaudeCodeVersion) bool {
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

func (v ClaudeCodeVersion) String() string {
	parts := make([]string, len(v))
	for i, n := range v {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ".")
}

// The installed Claude Code's version is kept, by binary, in memory and in
// a file (cacheFile) so short-lived commands share it: the scheduler asks on
// every pass, CLI commands and the setup window on each run. It's asked
// again when the binary changes (an update replaces it), after an hour (a
// version manager's shim doesn't change when what it runs does), and a
// failure is kept for a minute (a claude that hangs on --version mustn't
// hold up every pass).
const (
	claudeVersionTTL      = time.Hour
	claudeVersionErrorTTL = time.Minute
)

type cachedVersion struct {
	Stamp   string    `json:"stamp"`
	Version string    `json:"version,omitempty"`
	Error   string    `json:"error,omitempty"`
	At      time.Time `json:"at"`
}

func (c cachedVersion) fresh(stamp string, now time.Time) bool {
	ttl := claudeVersionTTL
	if c.Error != "" {
		ttl = claudeVersionErrorTTL
	}
	return c.Stamp == stamp && !c.At.After(now) && now.Sub(c.At) < ttl
}

func (c cachedVersion) result() (ClaudeCodeVersion, error) {
	if c.Error != "" {
		return nil, errors.New(c.Error)
	}
	return ParseClaudeCodeVersion(c.Version)
}

var claudeVersions = struct {
	sync.Mutex
	m map[string]cachedVersion
}{m: map[string]cachedVersion{}}

// runClaudeVersion runs `<path> --version`, with nothing on its standard
// input. Tests replace it (SetClaudeVersionForTests): they never run claude.
var runClaudeVersion = func(ctx context.Context, path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, path, "--version").Output() // stdin: /dev/null
}

// SetClaudeVersionForTests makes every claude report version (or fail with
// err) without running it; it returns the function that undoes it.
func SetClaudeVersionForTests(version func() (string, error)) (undo func()) {
	saved := runClaudeVersion
	runClaudeVersion = func(context.Context, string) ([]byte, error) {
		v, err := version()
		return []byte(v + " (Claude Code)"), err
	}
	claudeVersions.Lock()
	claudeVersions.m = map[string]cachedVersion{}
	claudeVersions.Unlock()
	return func() {
		runClaudeVersion = saved
		claudeVersions.Lock()
		claudeVersions.m = map[string]cachedVersion{}
		claudeVersions.Unlock()
	}
}

// InstalledClaudeCode is the version of the Claude Code at path, kept in
// cacheFile ("" keeps it in memory only).
func InstalledClaudeCode(ctx context.Context, path, cacheFile string) (ClaudeCodeVersion, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	stamp := fmt.Sprint(info.ModTime().UnixNano(), info.Size())
	now := time.Now()
	claudeVersions.Lock()
	c, ok := claudeVersions.m[path]
	claudeVersions.Unlock()
	if !ok && cacheFile != "" {
		c, ok = readVersionCache(cacheFile)[path]
	}
	if ok && c.fresh(stamp, now) {
		return c.result()
	}
	c = cachedVersion{Stamp: stamp, At: now}
	out, err := runClaudeVersion(ctx, path)
	var v ClaudeCodeVersion
	if err == nil {
		v, err = ParseClaudeCodeVersion(string(out))
	}
	if err != nil {
		err = fmt.Errorf("claude --version: %w", err)
		if ctx.Err() != nil {
			return nil, err // cancelled: not the binary's answer
		}
		c.Error = err.Error()
	} else {
		c.Version = v.String()
	}
	claudeVersions.Lock()
	claudeVersions.m[path] = c
	claudeVersions.Unlock()
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

// ClaudeCodeAt is the implementation for the Claude Code at path.
func ClaudeCodeAt(ctx context.Context, path, cacheFile string) (ClaudeCodeProtocol, error) {
	v, err := InstalledClaudeCode(ctx, path, cacheFile)
	if err != nil {
		return nil, err
	}
	return ClaudeCodeFor(v)
}
