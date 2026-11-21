package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/sysenv"
	"github.com/BorysSerbyn/process-automator/core/internal/usage"
)

// BackendDef is one model backend.
type BackendDef struct {
	// Name is its llm.backend value (set when it's looked up).
	Name string
	New  func(a *App) (agent.Backend, error)
	// AgentModel maps a routine's model (as written in its file) to what the
	// backend's agent CLI takes; "" means the CLI's default.
	AgentModel func(model string) (string, error)
	// Ready says what keeps it from running routines, in plain words ("" when
	// nothing does), and the sign-in that fixes it, when one does (setup).
	Ready func(a *App) (problem, signIn string)
	// PlanUsage reads the plan's usage limits, for a backend whose plan's
	// usage the app can read (claude-code); nil for others.
	PlanUsage func(ctx context.Context, a *App) ([]usage.Limit, error)
	// Priced is a backend paid per token (api): its runs have a cost, and
	// the llm.budget.* budgets apply. A backend neither priced nor with
	// PlanUsage has no usage limit in the app.
	Priced bool
	// CheckBuiltinTools says whether a routine's builtinTools are names the
	// backend's agent CLI takes; nil for a backend that takes none (its runs
	// refuse any).
	CheckBuiltinTools func(names []string) error
	// Experimental is a backend not yet tried against the real service: its
	// status says so wherever it's chosen (setup).
	Experimental bool
}

// Backends are the model Backends by llm.backend value. Adding one (another
// agent CLI, as an agent.CLITool) is one entry here plus its value in the
// setting's enum (a test keeps the two in sync).
var Backends = map[string]BackendDef{
	"api": {
		New: func(a *App) (agent.Backend, error) {
			key, err := a.Connector.Secrets.Get(AnthropicKey)
			if err != nil {
				return nil, errors.New("no Anthropic API key: run `" + product.Command + " auth login anthropic`, " +
					"or use your Claude subscription with `" + product.Command + " settings set llm.backend claude-code`")
			}
			return &agent.Genkit{APIKey: key}, nil
		},
		AgentModel: func(model string) (string, error) { return model, nil }, // Genkit takes the file's names
		Priced:     true,
		Ready: func(a *App) (string, string) {
			if _, err := a.Connector.Secrets.Get(AnthropicKey); err != nil {
				return "no Anthropic API key is stored", "anthropic"
			}
			return "", ""
		},
	},
	"claude-code": {
		New: func(a *App) (agent.Backend, error) {
			path, err := agent.FindClaude()
			if err != nil {
				return nil, err
			}
			token, _ := a.Connector.Secrets.Get(ClaudeCodeTokenKey) // optional
			return &agent.CLI{Tool: &agent.ClaudeCode{Path: path, OAuthToken: token}}, nil
		},
		AgentModel:        agent.ClaudeModel,
		CheckBuiltinTools: agent.CheckClaudeBuiltins,
		PlanUsage: func(ctx context.Context, a *App) ([]usage.Limit, error) {
			path, err := agent.FindClaude()
			if err != nil {
				return nil, err
			}
			token, _ := a.Connector.Secrets.Get(ClaudeCodeTokenKey)
			return (&agent.ClaudeCode{Path: path, OAuthToken: token}).PlanUsage(ctx)
		},
		Ready: func(a *App) (string, string) {
			if _, err := find(a, agent.FindClaude); err != nil {
				return "Claude Code isn't installed (no claude command found): install it from " +
					"https://claude.com/claude-code and sign in to it", ""
			}
			return "", ""
		},
	},
	"codex": {
		New: func(*App) (agent.Backend, error) {
			path, err := agent.FindCodex()
			if err != nil {
				return nil, err
			}
			return &agent.CLI{Tool: &agent.Codex{Path: path}}, nil
		},
		AgentModel:        agent.CodexModel,
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
