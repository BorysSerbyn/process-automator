package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
)

// CLI runs the model loop with a headless coding-agent CLI on the user's
// own subscription (Claude Code today; Codex, Gemini CLI or similar by adding a
// CLITool). What every such CLI shares lives here:
//
//   - the prompt is rendered with Genkit/Dotprompt, so routine files mean the
//     same thing on every backend;
//   - the allowed tools are served to the CLI from this process over a loopback
//     MCP server, so the gateway (allowlist, call log, dry-run) still applies;
//   - the CLI runs in the routine's folder, or an empty temporary one, and gets
//     the user prompt on stdin.
//
// A CLITool only says how to start its CLI headless and isolated from the
// user's own setup, and how to read its result.
type CLI struct {
	Tool CLITool
}

// CLITool is one agent CLI.
type CLITool interface {
	// Name is the backend name (the llm.backend setting value).
	Name() string
	// Command builds the command for one run. It must not give the CLI any tools
	// besides the MCP server in r.MCP, nor load the user's own settings.
	Command(ctx context.Context, r CLIRun) (*exec.Cmd, error)
	// Result reads the finished command's output into a result. runErr is the
	// command's exit error, if any.
	Result(stdout, stderr []byte, runErr error) (*Output, error)
}

// CLIRun is what a CLITool gets for one run.
type CLIRun struct {
	System string // system prompt (the routine's, or a neutral default)
	User   string // user prompt, also given on stdin
	// Model is the routine's model as written in its frontmatter
	// (e.g. "anthropic/claude-sonnet-5-5"); empty means the CLI's default. The
	// tool maps it to its own naming, or rejects models it can't run.
	Model string
	// Effort is the routine's effort (low … max), "" for the CLI's default.
	Effort   string
	MCP      MCPEndpoint
	MaxTurns int
	// Folder is the run's working directory when the routine sets one ("" for
	// an empty temporary folder); a tool may read its project instructions.
	Folder string
	// SessionID, when set, keeps the session under this ID; otherwise nothing
	// of the session may be saved.
	SessionID string
	// BuiltinTools are the CLI's own tools to allow, explicitly. In a dry run
	// (DryRun) the tool must give only those that can't change anything.
	BuiltinTools []string
	DryRun       bool
	// OutputSchema is the routine's output schema, if any: the tool asks the
	// CLI for a result in that shape and returns it as Output.Structured.
	OutputSchema map[string]any
}

// MCPEndpoint is the loopback MCP server holding the run's allowed tools.
type MCPEndpoint struct {
	Name  string // server name the CLI should use
	URL   string // streamable HTTP endpoint
	Token string // send as "Authorization: Bearer <Token>"
}

// CLIServer is the MCP server name agent CLIs see the routine's tools under.
const CLIServer = "procauto"

// defaultSystemPrompt replaces the CLI's own (coding-agent) system prompt when
// the routine doesn't set one.
const defaultSystemPrompt = "You carry out an automation routine for the user, unattended: " +
	"nobody will answer questions, so finish the task with the tools provided, then reply as the routine asks."

// Name implements Backend.
func (b *CLI) Name() string { return b.Tool.Name() }

// Run implements Backend.
func (b *CLI) Run(ctx context.Context, j Job) (*Output, error) {
	g := newGenkit(ctx, j)
	p, err := genkit.LoadPromptFromSource(g, j.Source, j.Name, "")
	if err != nil {
		return nil, fmt.Errorf("prompt %s: %w", j.Name, err)
	}
	rendered, err := p.Render(ctx, j.Input)
	if err != nil {
		return nil, fmt.Errorf("prompt %s: render: %w", j.Name, err)
	}
	system, user, err := splitMessages(rendered.Messages)
	if err != nil {
		return nil, fmt.Errorf("prompt %s: %w", j.Name, err)
	}
	if system == "" {
		system = defaultSystemPrompt
	}
	if j.Instructions != "" {
		system += "\n\n" + j.Instructions
	}

	url, token, stop, err := serveTools(j.Tools)
	if err != nil {
		return nil, err
	}
	defer stop()

	cmd, err := b.Tool.Command(ctx, CLIRun{
		System: system, User: user, Model: rendered.Model, Effort: j.Effort, MaxTurns: j.MaxTurns,
		MCP:    MCPEndpoint{Name: CLIServer, URL: url, Token: token},
		Folder: j.Folder, SessionID: j.SessionID, BuiltinTools: j.BuiltinTools, DryRun: j.DryRun,
		OutputSchema: outputSchema(rendered),
	})
	if err != nil {
		return nil, err
	}
	if j.Folder != "" {
		if info, err := os.Stat(j.Folder); err != nil || !info.IsDir() {
			return nil, fmt.Errorf("folder %s doesn't exist (routines.%s.folder)", j.Folder, j.Name)
		}
		cmd.Dir = j.Folder
	} else {
		dir, err := os.MkdirTemp("", "procauto-run-")
		if err != nil {
			return nil, err
		}
		defer func() { _ = os.RemoveAll(dir) }()
		cmd.Dir = dir // empty: no project instructions or settings to pick up
	}
	cmd.Stdin = strings.NewReader(user)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	out, err := b.Tool.Result(stdout.Bytes(), stderr.Bytes(), runErr)
	if out != nil {
		out.Schema = outputSchema(rendered)
	}
	return out, err
}

// outputSchema is the rendered prompt's output schema (Dotprompt output:), or nil.
func outputSchema(opts *ai.GenerateActionOptions) map[string]any {
	if opts == nil || opts.Output == nil || len(opts.Output.JsonSchema) == 0 {
		return nil
	}
	return opts.Output.JsonSchema
}
