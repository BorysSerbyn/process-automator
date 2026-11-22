package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/secrets"
)

// OpenAICompatKey is the secret-store key of the OpenAI-compatible server's
// API key (optional: local servers need none).
const OpenAICompatKey = "openai-compat-api-key"

// OpenAICompatURLSetting is the setting with the OpenAI-compatible server's URL.
const OpenAICompatURLSetting = "llm.openai-compat.base-url"

// OpenAICompat is the OpenAI-compatible server the claude-api backend runs
// openai-compat/<model> models on: its URL from the settings, its API key
// when one is stored.
func (a *App) OpenAICompat() (agent.OpenAICompat, error) {
	vals, err := a.Settings()
	if err != nil {
		return agent.OpenAICompat{}, err
	}
	key, _, err := secrets.Lookup(a.Connector.Secrets, OpenAICompatKey)
	if err != nil {
		return agent.OpenAICompat{}, fmt.Errorf("can't read the OpenAI-compatible server's API key: %w", err)
	}
	return agent.OpenAICompat{BaseURL: vals.String(OpenAICompatURLSetting), APIKey: key}, nil
}

// apiModelProblem is what keeps a routine's model from running on the claude-api
// backend, as far as can be told without connecting: a provider the backend
// doesn't run, or one that isn't set up (no Anthropic API key; no URL for the
// OpenAI-compatible server). A routine without a model has no problem here:
// as before, its run fails, saying it names none.
func apiModelProblem(a *App, model string) *Problem {
	provider, err := agent.APIModel(model)
	if err != nil {
		return &Problem{Text: err.Error()}
	}
	switch provider {
	case agent.AnthropicProvider:
		switch _, stored, err := secrets.Lookup(a.Connector.Secrets, AnthropicKey); {
		case err != nil:
			return &Problem{Text: "its model " + model + " needs the Anthropic API key, which can't be read: " + err.Error()}
		case !stored:
			return &Problem{Text: "its model " + model + " needs an Anthropic API key, and none is stored", Fix: product.Command + " auth login anthropic"}
		}
	case agent.OpenAICompatProvider:
		c, err := a.OpenAICompat()
		if err == nil {
			err = c.CheckURL()
		}
		if err != nil {
			return &Problem{
				Text: "its model " + model + " needs the OpenAI-compatible server (experimental, untested), but " + err.Error() + " (" + OpenAICompatURLSetting + ")",
				Fix:  product.Command + " settings set " + OpenAICompatURLSetting + " <url>",
			}
		}
	}
	return nil
}

// apiReachModel asks the OpenAI-compatible server, for an openai-compat
// model, whether it answers and has the model (agent.OpenAICompat.Probe,
// bounded by agent.ProbeTimeout).
func apiReachModel(ctx context.Context, a *App, model string) *Problem {
	provider, err := agent.APIModel(model)
	if err != nil || provider != agent.OpenAICompatProvider {
		return nil
	}
	c, err := a.OpenAICompat()
	if err != nil || c.CheckURL() != nil {
		return nil // apiModelProblem says so
	}
	_, id, _ := strings.Cut(model, "/")
	if err := c.Probe(ctx, id); err != nil {
		return &Problem{Text: "its model " + model + " needs the OpenAI-compatible server: " + err.Error()}
	}
	return nil
}

// onlyOpenAICompat is whether the routines run only models on the
// OpenAI-compatible server: then the claude-api backend needs no Anthropic API key.
// With no routines it does (the backend's usual way).
func onlyOpenAICompat(a *App) bool {
	set, err := routines.Load(a.Layout.Routines())
	if err != nil {
		return false
	}
	compat := false
	for _, r := range set.Routines {
		switch provider, _ := agent.APIModel(r.Model); provider {
		case agent.AnthropicProvider:
			return false
		case agent.OpenAICompatProvider:
			compat = true
		}
	}
	return compat
}
