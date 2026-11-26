package agent

import (
	"context"
	"time"
)

// CodexProtocol is everything the codex backend asks of the Codex CLI: the
// command lines it runs, their environment, and how it reads what they
// print. Each implementation is for the Codex versions from Since() up to
// the next implementation's, so a Codex release that changes something the
// backend relies on gets a new implementation and the older ones stay.
//
// Codex is optional (an experimental backend), so no install requires it:
// the newest implementation's version is what a Codex should be at to start
// on the backend, said here only. A Codex older than every implementation
// kept is refused, by the backend's readiness and by a run.
type CodexProtocol interface {
	// Since is the oldest Codex version this implementation is for.
	Since() Version
	// ExecArgs is a headless run's command line, after the binary; the
	// prompt comes on standard input.
	ExecArgs(r CodexRun) []string
	// ExecEnv is a run's environment: base (the caller's), minus anything
	// that would bill an API key, plus the token for the app's MCP server
	// (never on the command line).
	ExecEnv(base []string, mcpToken string) []string
	// Efforts are the routine efforts Codex takes (as its reasoning effort,
	// under the same names).
	Efforts() []string
	// Builtins are the names a routine's builtinTools take on Codex.
	Builtins() *BuiltinSet
	// AllowBuiltins validates a routine's builtinTools and says which are
	// allowed, in a dry run only those that can't change anything.
	AllowBuiltins(names []string, dryRun bool) (map[string]bool, error)
	// ProjectInstructions is the file in a folder Codex takes project
	// instructions from (the core adds it to the instructions itself).
	ProjectInstructions() string
	// OutputSchema is a routine's output schema in the form Codex asks the
	// model for (written to the file ExecArgs gets as SchemaFile).
	OutputSchema(schema map[string]any) map[string]any
	// Structured reads a run's final message as the routine's output,
	// matching its own schema again.
	Structured(schema map[string]any, final string) (any, error)
	// Events reads a run's standard output.
	Events(stdout []byte) CodexEvents
	// StderrError is the error a failed run printed on standard error.
	StderrError(stderr string) string
	// RunError words a failed run's message: a usage limit, a missing
	// sign-in or tools that couldn't be reached say so plainly.
	RunError(msg string) error
	// LoginCommand is the command I run in a terminal to sign Codex in.
	LoginCommand() string
	// LoginStatusArgs is the command line that says whether Codex is signed
	// in, as runs read the sign-in (no network).
	LoginStatusArgs() []string
	// LoginProblem reads LoginStatusArgs' output: why Codex isn't signed in,
	// "" when it is.
	LoginProblem(output string, exitedZero bool) string
	// MCPAddArgs registers a stdio MCP server in Codex's own config (every
	// folder), MCPRemoveArgs removes it, and MCPListArgs lists them, which
	// MCPServer reads: the one named name (ok false when there's none).
	MCPAddArgs(name string, command []string) []string
	MCPRemoveArgs(name string) []string
	MCPListArgs() []string
	MCPServer(listOutput []byte, name string) (s ClaudeMCPServer, ok bool, err error)
}

// CodexRun is a headless run as the backend has worked it out; a protocol
// turns it into Codex's flags and config overrides.
type CodexRun struct {
	Instructions string          // the system prompt, with the folder's project instructions
	Builtins     map[string]bool // the built-in tools allowed (AllowBuiltins)
	MCP          MCPEndpoint     // its token goes in the environment (ExecEnv)
	ToolTimeout  time.Duration   // the longest one call to the app's tools may take
	SaveSession  bool
	Model        string // "" is Codex's default
	Effort       string // "" is the model's default
	SchemaFile   string // the output schema's file (OutputSchema), if any
}

// CodexEvents is what a run printed.
type CodexEvents struct {
	SessionID string
	Final     string // the last agent message
	Completed bool   // its turn completed
	// Failed is the turn's error, LastError the last error reported (it may
	// have been retried).
	Failed, LastError                       string
	InputTokens, CachedTokens, OutputTokens int
}

// codexProtocols are the implementations kept, oldest first.
var codexProtocols = []CodexProtocol{codex0_171_0{}}

// NewestCodex is the newest implementation: the Codex to start on the
// backend with.
func NewestCodex() CodexProtocol { return codexProtocols[len(codexProtocols)-1] }

// OldestCodex is the oldest implementation: older Codexes don't run.
func OldestCodex() CodexProtocol { return codexProtocols[0] }

// CodexFor is the implementation for Codex version v: the newest whose
// Since is v or older.
func CodexFor(v Version) (CodexProtocol, error) {
	if p, ok := protocolFor(codexProtocols, v); ok {
		return p, nil
	}
	return nil, &TooOldError{CLI: "Codex", Have: v, Need: OldestCodex().Since(),
		Update: "`npm install -g @openai/codex@latest`, or `brew upgrade --cask codex`"}
}

// CodexAt is the implementation for the Codex at path, its version kept in
// cacheFile (InstalledVersion).
func CodexAt(ctx context.Context, path, cacheFile string) (CodexProtocol, error) {
	v, err := InstalledVersion(ctx, path, cacheFile)
	if err != nil {
		return nil, err
	}
	return CodexFor(v)
}
