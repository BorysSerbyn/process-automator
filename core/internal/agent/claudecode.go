package agent

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/firebase/genkit/go/ai"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BorysSerbyn/process-automator/core/internal/product"
)

// ClaudeCode is the Claude Code CLI (`claude -p`) as a CLI tool, so
// routines run on the user's Claude subscription.
//
// It's isolated from the user's own setup: no settings, hooks, plugins or
// skills, a replaced system prompt, no memory features, and restricted mode
// (file tools confined to the working folder). The routine's MCP server is its
// only tool source, plus the built-in tools the routine allows explicitly. The
// session is saved only under the ID the core picks (otherwise nothing is
// saved, so runs stay out of my own Claude Code sessions). A routine's folder
// contributes its CLAUDE.md, which the core reads and adds to the system
// prompt: Claude Code's own loading would bring the folder's settings and
// hooks too. From the frontmatter only `model` applies; `config` (max_tokens,
// ...) doesn't.
type ClaudeCode struct {
	// Path is the claude binary.
	Path string
	// OAuthToken, if set, is passed as CLAUDE_CODE_OAUTH_TOKEN (from
	// `claude setup-token`); otherwise Claude Code uses its own login.
	OAuthToken string
	// Protocol is how this Claude Code's version is run (ClaudeCodeAt); nil
	// is the newest.
	Protocol ClaudeCodeProtocol
}

func (b *ClaudeCode) protocol() ClaudeCodeProtocol {
	if b.Protocol == nil {
		return NewestClaudeCode()
	}
	return b.Protocol
}

// Name implements CLITool.
func (*ClaudeCode) Name() string { return "claude-code" }

// ErrNoClaude is FindClaude's error when there's no claude.
var ErrNoClaude = errors.New("claude-code backend: the `claude` CLI isn't on PATH (https://claude.com/claude-code)")

// FindClaude locates the claude binary on PATH (which includes the user's
// login-shell PATH, whoever started the command: see sysenv).
func FindClaude() (string, error) {
	p, err := exec.LookPath("claude")
	if err != nil {
		return "", ErrNoClaude
	}
	return p, nil
}

// toolCallTimeout is how long an agent CLI lets one call to the app's tools
// run: an hour, so the routine's own timeout is what ends a run.
const toolCallTimeout = time.Hour

// Command implements CLITool.
func (b *ClaudeCode) Command(ctx context.Context, r CLIRun) (*exec.Cmd, error) {
	model, err := ClaudeModel(r.Model)
	if err != nil {
		return nil, err
	}
	builtins, err := claudeBuiltins(b.protocol().Builtins(), r.BuiltinTools, r.DryRun)
	if err != nil {
		return nil, err
	}
	system, err := withProjectInstructions(r.System, r.Folder, b.protocol().ProjectInstructions())
	if err != nil {
		return nil, err
	}
	run := ClaudeRun{
		System: system, Builtins: builtins, MCP: r.MCP, ToolTimeout: toolCallTimeout,
		MaxTurns: r.MaxTurns, Model: model, Effort: r.Effort,
	}
	if r.OutputSchema != nil {
		schema, err := json.Marshal(r.OutputSchema)
		if err != nil {
			return nil, err
		}
		run.Schema = string(schema)
	}
	if r.SaveSession {
		run.SessionID = r.SessionID
	}
	args := b.protocol().RunArgs(run)
	cmd := exec.CommandContext(ctx, b.Path, args...)
	cmd.Env = b.env()
	return cmd, nil
}

var builtinNameRE = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)

// claudeBuiltins validates the routine's built-in tool names and, in a dry
// run, keeps only the read-only ones: built-ins run inside Claude Code, where
// the gateway can't intercept them.
func claudeBuiltins(set *BuiltinSet, names []string, dryRun bool) ([]string, error) {
	var out []string
	for _, n := range names {
		if !builtinNameRE.MatchString(n) {
			return nil, fmt.Errorf("builtinTools: %q isn't a Claude Code tool name (e.g. Read, Grep, Bash)", n)
		}
		if dryRun && !set.readOnly(n) {
			continue
		}
		out = append(out, n)
	}
	return out, nil
}

// CheckClaudeBuiltins says whether builtinTools are names the claude-code
// backend takes (Claude Code's tool names: the form is all any version checks).
func CheckClaudeBuiltins(names []string) error {
	_, err := claudeBuiltins(NewestClaudeCode().Builtins(), names, false)
	return err
}

// withProjectInstructions adds the folder's project instructions file (the
// CLI's own: CLAUDE.md, AGENTS.md), if any, to the system prompt.
func withProjectInstructions(system, folder, file string) (string, error) {
	if folder == "" {
		return system, nil
	}
	path := filepath.Join(folder, file)
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return system, nil
	}
	if err != nil {
		return "", err
	}
	return system + "\n\n# Project instructions (" + path + ")\n\n" + strings.TrimSpace(string(b)), nil
}

// Result implements CLITool: Claude Code's `--output-format json` result.
func (b *ClaudeCode) Result(_ CLIRun, stdout, stderr []byte, runErr error) (*Output, error) {
	res, err := b.protocol().Result(stdout)
	if err != nil {
		msg := strings.TrimSpace(string(stderr) + "\n" + string(stdout))
		if limit := b.protocol().PlanLimit(msg, time.Now()); limit != nil {
			return nil, limit
		}
		if runErr != nil {
			return nil, fmt.Errorf("claude: %w: %s", runErr, msg)
		}
		return nil, fmt.Errorf("claude: unreadable output: %s", msg)
	}
	out := &Output{
		Text: res.Text, InputTokens: res.InputTokens, OutputTokens: res.OutputTokens,
		CachedTokens: res.CachedTokens, Structured: res.Structured,
	}
	if res.IsError || runErr != nil {
		msg := res.Text
		if msg == "" {
			msg = res.Subtype
		}
		if limit := b.protocol().PlanLimit(msg, time.Now()); limit != nil {
			return out, limit
		}
		msg += b.protocol().SignInHint(msg)
		return out, fmt.Errorf("claude: %s", msg)
	}
	return out, nil
}

// env is the caller's environment as this Claude Code's runs take it.
func (b *ClaudeCode) env() []string { return b.protocol().RunEnv(os.Environ(), b.OAuthToken) }

// startMessage is the user turn for prompts written entirely as system text.
const startMessage = "Run the routine now."

// splitMessages turns a rendered prompt into an agent CLI's system prompt (or
// instructions) and the user prompt. The CLIs take one user turn, so other
// roles aren't supported. A system-only prompt gets a short user turn that starts it.
func splitMessages(msgs []*ai.Message) (system, user string, err error) {
	var sys, usr []string
	for _, m := range msgs {
		switch m.Role {
		case ai.RoleSystem:
			sys = append(sys, m.Text())
		case ai.RoleUser:
			usr = append(usr, m.Text())
		default:
			return "", "", fmt.Errorf("agent CLI backends support only system and user messages, not %q", m.Role)
		}
	}
	if len(usr) == 0 {
		if len(sys) == 0 {
			return "", "", errors.New("prompt renders nothing")
		}
		usr = []string{startMessage}
	}
	return strings.Join(sys, "\n\n"), strings.Join(usr, "\n\n"), nil
}

// ClaudeModel maps a Genkit model name ("anthropic/claude-sonnet-5-5") to a
// Claude Code model ID. Empty means Claude Code's default.
func ClaudeModel(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	provider, id, ok := strings.Cut(name, "/")
	if !ok {
		return name, nil
	}
	if provider != AnthropicProvider {
		return "", fmt.Errorf("the claude-code backend runs only Claude models, not %q", name)
	}
	return id, nil
}

// serveTools exposes the gateway tools as an MCP server on a loopback port,
// guarded by a random bearer token. stop shuts it down.
func serveTools(tools []Tool) (url, token string, stop func(), err error) {
	srv := sdk.NewServer(&sdk.Implementation{Name: product.Command, Version: "routine"}, nil)
	for _, t := range tools {
		call := t.Call
		srv.AddTool(&sdk.Tool{
			Name: t.Name, Description: t.Description, InputSchema: t.InputSchema, Annotations: t.Annotations,
		}, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			args := map[string]any{}
			if len(req.Params.Arguments) > 0 {
				if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
					return textResult("invalid arguments: "+err.Error(), true), nil
				}
			}
			out, err := call(ctx, args)
			if err != nil {
				return textResult(err.Error(), true), nil
			}
			return out, nil
		})
	}

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", nil, err
	}
	token = hex.EncodeToString(b)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", nil, err
	}
	mcpHandler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv }, nil)
	hs := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mcpHandler.ServeHTTP(w, r)
	})}
	go func() { _ = hs.Serve(ln) }()
	return "http://" + ln.Addr().String() + "/mcp", token, func() { _ = hs.Close() }, nil
}
