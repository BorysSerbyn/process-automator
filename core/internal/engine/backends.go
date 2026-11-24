package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/secrets"
	"github.com/BorysSerbyn/process-automator/core/internal/statusline"
	"github.com/BorysSerbyn/process-automator/core/internal/sysenv"
	"github.com/BorysSerbyn/process-automator/core/internal/usage"
)

// BackendDef is one model backend.
type BackendDef struct {
	// Name is its llm.backend value (set when it's looked up).
	Name string
	// Settings are the settings it needs set (local: its server and model),
	// for setup to show with the backend.
	Settings []string
	New      func(a *App) (agent.Backend, error)
	// AgentModel maps a routine's model (as written in its file) to what the
	// backend's agent CLI takes, or Genkit runs; "" means the CLI's default.
	AgentModel func(a *App, model string) (string, error)
	// Ready says what keeps it from running routines, in plain words ("" when
	// nothing does), and the sign-in that fixes it, when one does (setup).
	Ready func(a *App) (problem, signIn string)
	// PlanUsage reads the plan's usage limits, for a backend whose plan's
	// usage the app can read (claude-code); nil for others.
	PlanUsage func(ctx context.Context, a *App) ([]usage.Limit, error)
	// Priced is a backend paid per token (claude-api): its runs have a cost, and
	// the llm.budget.* budgets apply. A backend neither priced nor with
	// PlanUsage has no usage limit in the app.
	Priced bool
	// ModelProviders are the providers a routine's model may name on it
	// (<provider>/<model>); DefaultModel says in words what runs when a
	// routine names none. A test checks AgentModel takes each provider.
	ModelProviders []string
	DefaultModel   string
	// Builtins are the names a routine's builtinTools take on it (nil: none).
	Builtins *agent.BuiltinSet
	// CheckBuiltinTools says whether a routine's builtinTools are names the
	// backend's agent CLI takes; nil for a backend that takes none (its runs
	// refuse any).
	CheckBuiltinTools func(names []string) error
	// Experimental is a backend not yet tried against the real service: its
	// status says so wherever it's chosen (setup).
	Experimental bool
	// ModelProblem says what keeps a routine's model from running on the
	// backend, without connecting to anything (nil when nothing does: the
	// model's provider is set up); nil for a backend that checks nothing.
	ModelProblem func(a *App, model string) *Problem
	// ReachModel connects to the server that runs a routine's model and says
	// why the model can't run there (nil when it can); nil for a backend
	// with nothing to reach. It connects, so only doctor and routines check
	// ask, never the scheduler.
	ReachModel func(ctx context.Context, a *App, model string) *Problem
}

// Backends are the model Backends by llm.backend value. Adding one (another
// agent CLI, as an agent.CLITool) is one entry here plus its value in the
// setting's enum (a test keeps the two in sync).
var Backends = map[string]BackendDef{
	"claude-api": {
		New: func(a *App) (agent.Backend, error) {
			// Each provider's set-up is checked when a routine's model needs it.
			key, _, err := secrets.Lookup(a.Connector.Secrets, AnthropicKey)
			if err != nil {
				return nil, fmt.Errorf("can't read the Anthropic API key: %w", err)
			}
			local, err := a.Local()
			if err != nil {
				return nil, err
			}
			return &agent.Genkit{Backend: agent.ClaudeAPIBackend, APIKey: key, Local: local}, nil
		},
		AgentModel: func(_ *App, model string) (string, error) { // Genkit takes the file's names
			provider, err := agent.APIModel(model)
			if provider == agent.LocalProvider {
				model = agent.LocalModel(model, "")
			}
			return model, err
		},
		ModelProviders: []string{agent.AnthropicProvider, agent.LocalProvider},
		DefaultModel:   "none: name one",
		Priced:         true,
		ModelProblem:   apiModelProblem,
		ReachModel:     apiReachModel,
		Ready: func(a *App) (string, string) {
			_, stored, err := secrets.Lookup(a.Connector.Secrets, AnthropicKey)
			switch {
			case err != nil:
				return "the Anthropic API key can't be read: " + err.Error(), ""
			case stored || onlyLocal(a):
				return "", ""
			}
			return "no Anthropic API key is stored", "anthropic"
		},
	},
	"claude-code": {
		New: func(a *App) (agent.Backend, error) {
			cc, err := claudeCode(a)
			if err != nil {
				return nil, err
			}
			return &agent.CLI{Tool: cc}, nil
		},
		AgentModel:        func(_ *App, model string) (string, error) { return agent.ClaudeModel(model) },
		ModelProviders:    []string{agent.AnthropicProvider},
		DefaultModel:      "Claude Code's default",
		Builtins:          agent.NewestClaudeCode().Builtins(),
		CheckBuiltinTools: agent.CheckClaudeBuiltins,
		PlanUsage: func(ctx context.Context, a *App) ([]usage.Limit, error) {
			cc, err := claudeCode(a)
			if err != nil {
				return nil, err
			}
			return cc.PlanUsage(ctx)
		},
		Ready: func(a *App) (string, string) {
			if _, _, err := FindClaudeCode(a); err != nil {
				return claudeCodeProblem(err), ""
			}
			return "", ""
		},
	},
	"local": {
		New:      newLocal,
		Settings: []string{LocalURLSetting, LocalModelSetting},
		// Routine files name Claude models: they run on llm.local.model
		// unless they name local/<model>.
		AgentModel:     func(a *App, model string) (string, error) { return agent.LocalModel(model, a.localModel()), nil },
		ModelProviders: []string{agent.LocalProvider},
		DefaultModel:   "llm.local.model, which also runs every model that isn't local/<model>",
		Experimental:   true, // tried only with a fake server
		ModelProblem:   localModelProblem,
		ReachModel:     localReachModel,
		Ready:          localReady,
	},
	"codex": {
		New: func(*App) (agent.Backend, error) {
			path, err := agent.FindCodex()
			if err != nil {
				return nil, err
			}
			return &agent.CLI{Tool: &agent.Codex{Path: path}}, nil
		},
		AgentModel:        func(_ *App, model string) (string, error) { return agent.CodexModel(model) },
		ModelProviders:    []string{agent.OpenAIProvider},
		DefaultModel:      "Codex's default",
		Builtins:          &agent.CodexBuiltins,
		CheckBuiltinTools: agent.CheckCodexBuiltins,
		Experimental:      true, // tried only with a fake model provider
		Ready: func(a *App) (string, string) {
			path, err := find(a, agent.FindCodex)
			if err != nil {
				return "Codex isn't installed (no codex command found): install it from " +
					"https://developers.openai.com/codex/cli and sign in to it with `codex login`", ""
			}
			if problem := codexLogin.check(path); problem != "" {
				return "Codex isn't signed in (" + problem + "): run `codex login` in a terminal", ""
			}
			return "", ""
		},
	},
}

// FindClaudeCode is the installed Claude Code: its binary, and the protocol
// for its version. Tests replace it: they never run the real claude.
var FindClaudeCode = func(a *App) (string, agent.ClaudeCodeProtocol, error) {
	path, err := find(a, agent.FindClaude)
	if err != nil {
		return "", nil, err
	}
	protocol, err := agent.ClaudeCodeAt(context.Background(), path)
	if err != nil {
		return "", nil, err
	}
	return path, protocol, nil
}

// ClaudeStatusLine is where the installed Claude Code keeps its status line.
func ClaudeStatusLine(a *App) (statusline.Format, error) {
	_, protocol, err := FindClaudeCode(a)
	if err != nil {
		return statusline.Format{}, err
	}
	return protocol.StatusLine()
}

// claudeCodeProblem says what's wrong with Claude Code, from FindClaudeCode's error.
func claudeCodeProblem(err error) string {
	var old *agent.ClaudeCodeTooOld
	if errors.As(err, &old) {
		return err.Error()
	}
	if errors.Is(err, agent.ErrNoClaude) {
		return "Claude Code isn't installed (no claude command found): install it from " +
			"https://claude.com/claude-code and sign in to it"
	}
	return "Claude Code can't be used: " + err.Error()
}

// claudeCode is the claude CLI, with the `claude setup-token` token when
// one is stored (optional: without it, Claude Code's own login).
func claudeCode(a *App) (*agent.ClaudeCode, error) {
	path, protocol, err := FindClaudeCode(a)
	if err != nil {
		return nil, err
	}
	token, _, err := secrets.Lookup(a.Connector.Secrets, ClaudeCodeTokenKey)
	if err != nil {
		return nil, fmt.Errorf("can't read the Claude Code token: %w", err)
	}
	return &agent.ClaudeCode{Path: path, OAuthToken: token, Protocol: protocol}, nil
}

// find looks for an agent CLI's binary; when it isn't on PATH it may have
// been installed since the login PATH was saved, so that's saved again (at
// most once a minute: the scheduler asks every pass) and it's looked for again.
func find(a *App, look func() (string, error)) (string, error) {
	if p, err := look(); err == nil {
		return p, nil
	}
	cache := a.Layout.LoginPath()
	if info, err := os.Stat(cache); err != nil || time.Since(info.ModTime()) > time.Minute {
		_ = sysenv.SaveLoginShellPath(cache)
	}
	return look()
}

// CodexLoginStatus is how Codex's sign-in is checked (`codex login status`);
// tests replace it.
var CodexLoginStatus = agent.CodexLoginStatus

// codexLogin remembers Codex's sign-in for a minute: the scheduler asks every
// pass, and each check runs codex.
var codexLogin loginCache

type loginCache struct {
	mu      sync.Mutex
	path    string
	at      time.Time
	problem string
}

// check is why the CLI at path isn't signed in ("" when it is, or when that
// can't be told).
func (c *loginCache) check(path string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.path == path && time.Since(c.at) < time.Minute {
		return c.problem
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	problem, err := CodexLoginStatus(ctx, path)
	if err != nil {
		problem = "" // can't tell: a run says so if it isn't
	}
	c.path, c.at, c.problem = path, time.Now(), problem
	return problem
}

// BackendDef is the backend chosen by the llm.backend setting.
func (a *App) BackendDef() (BackendDef, error) {
	vals, err := a.Settings()
	if err != nil {
		return BackendDef{}, err
	}
	name := vals.String("llm.backend")
	def, ok := Backends[name]
	if !ok {
		return BackendDef{}, fmt.Errorf("unknown llm.backend %q", name)
	}
	def.Name = name
	return def, nil
}

// BackendState is a backend and what keeps it from running routines
// (problem "" when nothing does), with the sign-in that fixes it.
type BackendState struct {
	Name, Problem, SignIn string
	Experimental          bool
}

// State says whether the backend can run routines now.
func (d BackendDef) State(a *App) BackendState {
	b := BackendState{Name: d.Name, Experimental: d.Experimental}
	b.Problem, b.SignIn = d.Ready(a)
	return b
}

// Status is the backend and its state in words.
func (b BackendState) Status() string {
	name := b.Name
	if b.Experimental {
		name += " (experimental, untested)"
	}
	if b.Problem != "" {
		return "Runs with " + name + ", but " + b.Problem
	}
	return "Runs with " + name + ", ready"
}

// NewBackend builds the model backend chosen by the llm.backend setting.
func (a *App) NewBackend() (agent.Backend, error) {
	def, err := a.BackendDef()
	if err != nil {
		return nil, err
	}
	return def.New(a)
}

// BackendReady is the configured backend and whether it can run routines.
func (a *App) BackendReady() (BackendState, error) {
	def, err := a.BackendDef()
	if err != nil {
		return BackendState{}, err
	}
	return def.State(a), nil
}
