package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// Codex is OpenAI's Codex CLI (`codex exec`) as a CLI tool, so routines run
// on the user's own Codex sign-in (`codex login`: a ChatGPT plan or an API key).
//
// It's isolated from the user's own setup as far as Codex allows: their
// config.toml isn't loaded (--ignore-user-config: no MCP servers, profiles,
// model or provider settings, hooks; the sign-in still comes from CODEX_HOME),
// nor their execpolicy rules (--ignore-rules), and the folder's own Codex
// config stays off (Codex loads a project's config only for a project trusted
// in the user's config, which isn't read). Codex's own AGENTS.md loading is
// off: the folder's AGENTS.md is added to the instructions by the core, as
// Claude Code's CLAUDE.md is. Codex's global AGENTS.md (in CODEX_HOME) still
// loads: nothing turns it off short of another CODEX_HOME, which would leave
// the sign-in behind.
//
// The routine's MCP server is its only MCP server, with its tools approved up
// front (the gateway decides what runs). Codex's apps, plugins, subagents,
// image generation, browser and computer use are off; its own tools (shell,
// file edits, web search, image viewing) are what builtinTools allows, in a
// sandbox that's read-only unless the routine allows write. Nothing ever
// asks for approval (approval_policy never: what would need one is
// refused). Codex picks a saved session's ID and reports it; otherwise
// nothing of the session is saved (--ephemeral). From the frontmatter only
// `model` applies.
//
// What Codex's flags, config keys, environment and output are is its
// version's CodexProtocol (codexprotocol.go); this is what any version does
// with them.
type Codex struct {
	// Path is the codex binary.
	Path string
	// Protocol is how this Codex's version is run (CodexAt); nil is the newest.
	Protocol CodexProtocol
}

func (b *Codex) protocol() CodexProtocol {
	if b.Protocol == nil {
		return NewestCodex()
	}
	return b.Protocol
}

// Name implements CLITool.
func (*Codex) Name() string { return "codex" }

// PicksSessionID implements SessionPicker: Codex names its sessions (threads).
func (*Codex) PicksSessionID() bool { return true }

// ErrNoCodex is FindCodex's error when there's no codex.
var ErrNoCodex = errors.New("codex backend: the `codex` CLI isn't on PATH (https://developers.openai.com/codex/cli)")

// FindCodex locates the codex binary on PATH (which includes the user's
// login-shell PATH, whoever started the command: see sysenv).
func FindCodex() (string, error) {
	p, err := exec.LookPath("codex")
	if err != nil {
		return "", ErrNoCodex
	}
	return p, nil
}

// CheckCodexBuiltins says whether builtinTools are names the codex backend
// takes (the newest implementation's).
func CheckCodexBuiltins(names []string) error {
	_, err := NewestCodex().AllowBuiltins(names, false)
	return err
}

// OpenAIProvider names OpenAI's models in a routine's model (openai/<model>),
// the only ones Codex runs.
const OpenAIProvider = "openai"

// CodexModel maps a Genkit model name ("openai/gpt-5.5") to a Codex model ID.
// Empty means Codex's default.
func CodexModel(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	provider, id, ok := strings.Cut(name, "/")
	if !ok {
		return name, nil
	}
	if provider != OpenAIProvider {
		return "", fmt.Errorf("the codex backend runs only OpenAI models (%s/<model>), not %q", OpenAIProvider, name)
	}
	return id, nil
}

// codexEffort checks a routine's effort is one p takes ("": the model's default).
func codexEffort(p CodexProtocol, e string) (string, error) {
	if e == "" || slices.Contains(p.Efforts(), e) {
		return e, nil
	}
	return "", fmt.Errorf("effort %q isn't one Codex takes (%s)", e, strings.Join(p.Efforts(), ", "))
}

// Command implements CLITool.
func (b *Codex) Command(ctx context.Context, r CLIRun) (*exec.Cmd, error) {
	p := b.protocol()
	model, err := CodexModel(r.Model)
	if err != nil {
		return nil, err
	}
	effort, err := codexEffort(p, r.Effort)
	if err != nil {
		return nil, err
	}
	builtins, err := p.AllowBuiltins(r.BuiltinTools, r.DryRun)
	if err != nil {
		return nil, err
	}
	instructions, err := withProjectInstructions(r.System, r.Folder, p.ProjectInstructions())
	if err != nil {
		return nil, err
	}
	run := CodexRun{
		Instructions: instructions, Builtins: builtins, MCP: r.MCP, ToolTimeout: toolCallTimeout,
		SaveSession: r.SaveSession, Model: model, Effort: effort,
	}
	if r.OutputSchema != nil {
		if run.SchemaFile, err = writeCodexSchema(r.TempDir, p.OutputSchema(r.OutputSchema)); err != nil {
			return nil, err
		}
	}
	cmd := exec.CommandContext(ctx, b.Path, p.ExecArgs(run)...)
	cmd.Env = p.ExecEnv(os.Environ(), r.MCP.Token)
	return cmd, nil
}

// writeCodexSchema writes the output schema, in Codex's form, to a file in dir.
func writeCodexSchema(dir string, schema map[string]any) (string, error) {
	if dir == "" {
		return "", errors.New("codex: no folder for the output schema")
	}
	b, err := json.Marshal(schema)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "output-schema.json")
	return path, os.WriteFile(path, b, 0o600)
}

// Result implements CLITool: the run's events (the protocol's Events). The
// run succeeded when its turn completed with a final message.
func (b *Codex) Result(r CLIRun, stdout, stderr []byte, runErr error) (*Output, error) {
	p := b.protocol()
	ev := p.Events(stdout)
	out := &Output{Text: ev.Final, SessionID: ev.SessionID,
		InputTokens: ev.InputTokens, CachedTokens: ev.CachedTokens, OutputTokens: ev.OutputTokens}
	if !r.SaveSession {
		out.SessionID = ""
	}
	if runErr == nil && ev.Failed == "" && ev.Completed && strings.TrimSpace(ev.Final) != "" {
		if r.OutputSchema != nil {
			v, err := p.Structured(r.OutputSchema, ev.Final)
			if err != nil {
				return out, fmt.Errorf("codex: the result isn't the JSON the routine's output schema asks for: %w", err)
			}
			out.Structured = v
		}
		return out, nil
	}
	msg := ev.Failed
	if msg == "" {
		msg = ev.LastError
	}
	if msg == "" {
		msg = p.StderrError(string(stderr))
	}
	switch {
	case msg == "" && runErr != nil:
		msg = runErr.Error()
	case msg == "" && ev.Completed:
		msg = "the run ended without a final message"
	case msg == "":
		msg = "the run ended before its turn completed"
	}
	return out, p.RunError(msg)
}

// CodexLoginStatus runs p's login status check on the codex at path: "" when
// it's signed in, else why not.
func CodexLoginStatus(ctx context.Context, path string, p CodexProtocol) (problem string, err error) {
	cmd := exec.CommandContext(ctx, path, p.LoginStatusArgs()...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return p.LoginProblem(out.String(), true), nil
	case errors.As(err, &exit):
		return p.LoginProblem(out.String(), false), nil
	}
	return "", err
}
