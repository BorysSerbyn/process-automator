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

// Model providers the api backend runs, as a routine's model names them
// (<provider>/<model>).
const (
	// AnthropicProvider is the Anthropic API (pay per token, an API key).
	AnthropicProvider = "anthropic"
	// OpenAICompatProvider is the one OpenAI-compatible server the settings
	// name (Ollama, llama.cpp's llama-server, vLLM, LM Studio, or a hosted
	// API): experimental, untested.
	OpenAICompatProvider = "openai-compat"
)

// Genkit runs the model loop in-process with Genkit: anthropic/<model> on the
// Anthropic API, openai-compat/<model> on an OpenAI-compatible server. The
// frontmatter's model and config apply.
type Genkit struct {
	// APIKey is the Anthropic API key; "" when none is stored (only
	// openai-compat models run then).
	APIKey string
	// OpenAICompat is the OpenAI-compatible server.
	OpenAICompat OpenAICompat
}

// Name implements Backend.
func (*Genkit) Name() string { return "api" }

// APIModel checks that a routine's model is one the api backend runs
// (anthropic/<model> or openai-compat/<model>), and returns its provider.
// No model ("") is left to Genkit, which has no default: a run fails.
func APIModel(model string) (provider string, err error) {
	if model == "" {
		return "", nil
	}
	provider, id, ok := strings.Cut(model, "/")
	switch {
	case !ok || id == "":
		return "", fmt.Errorf("its model %q names no provider: the api backend runs %s/<model> and %s/<model> models", model, AnthropicProvider, OpenAICompatProvider)
	case provider != AnthropicProvider && provider != OpenAICompatProvider:
		return "", fmt.Errorf("its model %q isn't one the api backend runs: it runs %s/<model> and %s/<model> models", model, AnthropicProvider, OpenAICompatProvider)
	}
	return provider, nil
}

// Run implements Backend.
func (b *Genkit) Run(ctx context.Context, j Job) (*Output, error) {
	if j.Effort != "" {
		return nil, fmt.Errorf("effort %q isn't supported by the api backend yet; leave it at default or use an agent CLI backend (claude-code or codex)", j.Effort)
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
	if b.OpenAICompat.BaseURL != "" {
		plugins = append(plugins, &compat_oai.OpenAICompatible{
			Provider: OpenAICompatProvider, BaseURL: b.OpenAICompat.BaseURL, APIKey: b.OpenAICompat.APIKey,
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
	provider, err := b.provider(req.Model)
	if err != nil {
		return nil, err
	}
	if j.Instructions != "" {
		req.Messages = withSystemText(req.Messages, j.Instructions)
	}
	req.MaxTurns = j.MaxTurns
	req.Tools = nil
	for _, t := range j.Tools {
		req.Tools = append(req.Tools, t.Name)
	}
	if provider == OpenAICompatProvider {
		// A tool error goes back to the model, as on agent CLI backends,
		// including input that doesn't fit the tool's schema or a tool that
		// doesn't exist (smaller models get these wrong more often): the
		// outcome rules still fail a run whose write didn't happen.
		req.Use = append(req.Use, &ai.MiddlewareRef{Name: middleware.SoftToolErrors{}.Name()})
	}
	resp, err := genkit.GenerateWithRequest(ctx, g, req, nil, nil)
	if err != nil {
		if provider == OpenAICompatProvider {
			return nil, b.OpenAICompat.plainError(err)
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
		out.Model = req.Model
		// An OpenAI-compatible server's models have no list price: their
		// cost is unknown, and isn't counted toward the LLM budgets.
		if provider == AnthropicProvider {
			out.CostUSD, out.Priced = apiCost(req.Model, u.InputTokens, u.OutputTokens, u.CachedContentTokens, u.CacheWriteTokens)
		}
	}
	return out, nil
}

// provider is the provider that runs model, once it's set up.
func (b *Genkit) provider(model string) (string, error) {
	if model == "" {
		return "", errors.New("the routine names no model, and the api backend has no default: " +
			"set its model (e.g. " + AnthropicProvider + "/claude-sonnet-5-5, or " + OpenAICompatProvider + "/<model> for an OpenAI-compatible server)")
	}
	provider, err := APIModel(model)
	if err != nil {
		return "", err
	}
	switch {
	case provider == AnthropicProvider && b.APIKey == "":
		return "", fmt.Errorf("its model %s needs an Anthropic API key, and none is stored: run `%s auth login anthropic`, "+
			"or use your Claude subscription with `%s settings set llm.backend claude-code`", model, product.Command, product.Command)
	case provider == OpenAICompatProvider && b.OpenAICompat.BaseURL == "":
		return "", fmt.Errorf("its model %s needs the OpenAI-compatible server's URL: `%s settings set llm.openai-compat.base-url <url>`", model, product.Command)
	}
	return provider, nil
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
