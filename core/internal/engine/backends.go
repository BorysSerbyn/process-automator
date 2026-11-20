package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
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
	// PlanUsage reads the plan's usage limits, for a backend on a plan
	// (claude-code); nil for one that's paid per token (api).
	PlanUsage func(ctx context.Context, a *App) ([]usage.Limit, error)
}

// Backends are the model Backends by llm.backend value. Adding one (e.g.
// another agent CLI such as Codex, as an agent.CLITool) is one entry here plus
// its value in the setting's enum (a test keeps the two in sync).
var Backends = map[string]BackendDef{
	"api": {
		New: func(a *App) (agent.Backend, error) {
			key, err := a.Connector.Secrets.Get(AnthropicKey)
			if err != nil {
				return nil, errors.New("no Anthropic API key: run `procauto auth login anthropic`, " +
					"or use your Claude subscription with `procauto settings set llm.backend claude-code`")
			}
			return &agent.Genkit{APIKey: key}, nil
		},
		AgentModel: func(model string) (string, error) { return model, nil }, // Genkit takes the file's names
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
		AgentModel: agent.ClaudeModel,
		PlanUsage: func(ctx context.Context, a *App) ([]usage.Limit, error) {
			path, err := agent.FindClaude()
			if err != nil {
				return nil, err
			}
			token, _ := a.Connector.Secrets.Get(ClaudeCodeTokenKey)
			return (&agent.ClaudeCode{Path: path, OAuthToken: token}).PlanUsage(ctx)
		},
		Ready: func(a *App) (string, string) {
			if _, err := agent.FindClaude(); err == nil {
				return "", ""
			}
			// Maybe installed since the login PATH was saved: look again, at
			// most once a minute (the scheduler asks every pass).
			cache := a.Layout.LoginPath()
			if info, err := os.Stat(cache); err != nil || time.Since(info.ModTime()) > time.Minute {
				_ = sysenv.SaveLoginShellPath(cache)
			}
			if _, err := agent.FindClaude(); err != nil {
				return "Claude Code isn't installed (no claude command found): install it from " +
					"https://claude.com/claude-code and sign in to it", ""
			}
			return "", ""
		},
	},
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
}

// State says whether the backend can run routines now.
func (d BackendDef) State(a *App) BackendState {
	b := BackendState{Name: d.Name}
	b.Problem, b.SignIn = d.Ready(a)
	return b
}

// Status is the backend and its state in words.
func (b BackendState) Status() string {
	if b.Problem != "" {
		return "Runs with " + b.Name + ", but " + b.Problem
	}
	return "Runs with " + b.Name + ", ready"
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
