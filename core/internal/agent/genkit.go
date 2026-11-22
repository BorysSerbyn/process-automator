package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/core/api"
	"github.com/firebase/genkit/go/genkit"
	"github.com/firebase/genkit/go/plugins/anthropic"
	"github.com/firebase/genkit/go/plugins/compat_oai"
	"github.com/firebase/genkit/go/plugins/middleware"

	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// The backends Genkit runs (their llm.backend values).
const (
	// ClaudeAPIBackend runs Claude on Anthropic's API, and routines whose
	// model is local/<model> on the local server.
	ClaudeAPIBackend = "claude-api"
	// LocalBackend runs every routine on the local server.
	LocalBackend = "local"
)

// Model providers Genkit runs, as a routine's model names them
// (<provider>/<model>).
const (
	// AnthropicProvider is the Anthropic API (pay per token, an API key).
	AnthropicProvider = "anthropic"
	// LocalProvider is my own OpenAI-compatible server, the one in
	// llm.local.url (Ollama, llama.cpp's llama-server, vLLM, LM Studio, or a
	// hosted API): experimental, untested.
	LocalProvider = "local"
	// FormerLocalProvider is LocalProvider's former name, still taken.
	FormerLocalProvider = "openai-compat"
)

// Genkit runs the model loop in-process with Genkit: on claude-api,
// anthropic/<model> on the Anthropic API and local/<model> on the local
// server; on local, every routine on the local server. The frontmatter's
// config applies.
type Genkit struct {
	// Backend is the llm.backend it is: ClaudeAPIBackend ("" too) or
	// LocalBackend.
	Backend string
	// APIKey is the Anthropic API key; "" when none is stored (only local
	// models run then).
	APIKey string
	// Local is my own OpenAI-compatible server.
	Local OpenAICompat
	// LocalModel is, on the local backend, the server's model routines run
	// on (llm.local.model), whatever model they name, unless they name
	// local/<model>.
	LocalModel string
}

// Name implements Backend.
func (b *Genkit) Name() string {
	if b.Backend == "" {
		return ClaudeAPIBackend
	}
	return b.Backend
}

// APIModel checks that a routine's model is one the claude-api backend runs
// (anthropic/<model> or local/<model>; openai-compat/<model> is local's
// former name), and returns its provider (LocalProvider for either).
// No model ("") is left to Genkit, which has no default: a run fails.
func APIModel(model string) (provider string, err error) {
	if model == "" {
		return "", nil
	}
	provider, id, ok := strings.Cut(model, "/")
	switch {
	case !ok || id == "":
		return "", fmt.Errorf("its model %q names no provider: the claude-api backend runs %s/<model> and %s/<model> models", model, AnthropicProvider, LocalProvider)
	case provider == FormerLocalProvider:
		return LocalProvider, nil
	case provider != AnthropicProvider && provider != LocalProvider:
		return "", fmt.Errorf("its model %q isn't one the claude-api backend runs: it runs %s/<model> and %s/<model> models", model, AnthropicProvider, LocalProvider)
	}
	return provider, nil
}

// LocalModel is the model a routine runs on the local server, as
// local/<model>: the one it names as local/<model> (or
// openai-compat/<model>), else def (llm.local.model on the local backend);
// "" when neither.
func LocalModel(model, def string) string {
	if provider, id, ok := strings.Cut(model, "/"); ok && id != "" && (provider == LocalProvider || provider == FormerLocalProvider) {
		return LocalProvider + "/" + id
	}
	if def == "" {
		return ""
	}
	return LocalProvider + "/" + def
}

// Run implements Backend.
func (b *Genkit) Run(ctx context.Context, j Job) (*Output, error) {
	if j.Effort != "" {
		return nil, fmt.Errorf("effort %q isn't supported by the %s backend yet; leave it at default or use an agent CLI backend (claude-code or codex)", j.Effort, b.Name())
	}
	if j.Folder != "" || j.SaveSession || j.SessionID != "" || len(j.BuiltinTools) > 0 {
		return nil, errors.New("folder, session and builtinTools need an agent CLI backend (llm.backend claude-code or codex)")
	}
	// Only the providers that are set up are registered: Genkit's Anthropic
	// plugin refuses to start without a key.
	plugins := []api.Plugin{&middleware.Middleware{}}
	if b.APIKey != "" {
		plugins = append(plugins, &anthropic.Anthropic{APIKey: b.APIKey})
	}
	if b.Local.BaseURL != "" {
		plugins = append(plugins, &compat_oai.OpenAICompatible{
			Provider: LocalProvider, BaseURL: b.Local.BaseURL, APIKey: b.Local.APIKey,
		})
	}
	g := newGenkit(ctx, j, genkit.WithPlugins(plugins...))
	for _, t := range j.Tools {
		call := t.Call
		genkit.DefineTool(g, t.Name, t.Description,
			func(tc *ai.ToolContext, input any) (string, error) {
				args, err := toMap(input)
				if err != nil {
					return "", err
				}
				out, err := call(tc, args)
				if err != nil {
					return "", err
				}
				if out.IsError {
					return "Tool error: " + pluginapi.ResultText(out), nil
				}
				return pluginapi.ResultText(out), nil
			},
			ai.WithInputSchema(t.InputSchema),
		)
	}
	p, err := genkit.LoadPromptFromSource(g, j.Source, j.Name, "")
	if err != nil {
		return nil, fmt.Errorf("prompt %s: %w", j.Name, err)
	}
	req, err := p.Render(ctx, j.Input)
	if err != nil {
		return nil, fmt.Errorf("prompt %s: render: %w", j.Name, err)
	}
	model, provider, err := b.model(req.Model)
	if err != nil {
		return nil, err
	}
	req.Model = model
	if j.Instructions != "" {
		req.Messages = withSystemText(req.Messages, j.Instructions)
	}
	req.MaxTurns = j.MaxTurns
	req.Tools = nil
	for _, t := range j.Tools {
		req.Tools = append(req.Tools, t.Name)
	}
	if provider == LocalProvider {
		// A tool error goes back to the model, as on agent CLI backends,
		// including input that doesn't fit the tool's schema or a tool that
		// doesn't exist (smaller models get these wrong more often): the
		// outcome rules still fail a run whose write didn't happen.
		req.Use = append(req.Use, &ai.MiddlewareRef{Name: middleware.SoftToolErrors{}.Name()})
	}
	resp, err := genkit.GenerateWithRequest(ctx, g, req, nil, nil)
	if err != nil {
		if provider == LocalProvider {
			return nil, b.Local.plainError(err)
		}
		return nil, err
	}
	out := &Output{Text: resp.Text()}
	if schema := outputSchema(req); schema != nil {
		out.Schema = schema
		var v any
		if err := resp.Output(&v); err == nil {
			out.Structured = v
		}
	}
	u := resp.TotalUsage
	if u == nil {
		u = resp.Usage
	}
	if u != nil {
		out.InputTokens, out.OutputTokens, out.CachedTokens = u.InputTokens, u.OutputTokens, u.CachedContentTokens
		// The local server's models have no list price: their cost is
		// unknown, and isn't counted toward the LLM budgets (no warning:
		// there's no price to miss).
		if provider == AnthropicProvider {
			out.Model = req.Model
			out.CostUSD, out.Priced = apiCost(req.Model, u.InputTokens, u.OutputTokens, u.CachedContentTokens, u.CacheWriteTokens)
		}
	}
	return out, nil
}

// model is the model a routine that names model runs (as Genkit names it),
// and its provider, once that's set up.
func (b *Genkit) model(model string) (string, string, error) {
	if b.Backend == LocalBackend {
		model = LocalModel(model, b.LocalModel)
		if model == "" {
			return "", "", fmt.Errorf("the local backend has no model to run: `%s settings set llm.local.model <model>` (its name on the server, e.g. qwen3:8b)", product.Command)
		}
	}
	if model == "" {
		return "", "", errors.New("the routine names no model, and the claude-api backend has no default: " +
			"set its model (e.g. " + AnthropicProvider + "/claude-sonnet-5-5, or " + LocalProvider + "/<model> for the local server)")
	}
	provider, err := APIModel(model)
	if err != nil {
		return "", "", err
	}
	switch {
	case provider == AnthropicProvider && b.APIKey == "":
		return "", "", fmt.Errorf("its model %s needs an Anthropic API key, and none is stored: run `%s auth login anthropic`, "+
			"or use your Claude subscription with `%s settings set llm.backend claude-code`", model, product.Command, product.Command)
	case provider == LocalProvider && b.Local.BaseURL == "":
		return "", "", fmt.Errorf("its model %s needs the local server's URL: `%s settings set llm.local.url <url>`", model, product.Command)
	case provider == LocalProvider:
		model = LocalModel(model, "") // openai-compat/<model> is local/<model>
	}
	return model, provider, nil
}

// withSystemText adds text to the prompt's system message, creating one if needed.
func withSystemText(msgs []*ai.Message, text string) []*ai.Message {
	for _, m := range msgs {
		if m.Role == ai.RoleSystem {
			m.Content = append(m.Content, ai.NewTextPart("\n\n"+text))
			return msgs
		}
	}
	return append([]*ai.Message{ai.NewSystemTextMessage(text)}, msgs...)
}
