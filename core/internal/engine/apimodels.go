package engine

import (
	"context"
	"strings"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/secrets"
)

// apiModelProblem is what keeps a routine's model from running on the
// claude-api backend, as far as can be told without connecting: a provider
// the backend doesn't run, or one that isn't set up (no Anthropic API key; no
// URL for the local server). A routine without a model has no problem here:
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
	case agent.LocalProvider:
		c, err := a.Local()
		if err == nil {
			err = c.CheckURL()
		}
		if err != nil {
			return &Problem{
				Text: "its model " + model + " needs the local server (experimental, untested), but " + err.Error() + " (" + LocalURLSetting + ")",
				Fix:  product.Command + " settings set " + LocalURLSetting + " <url>",
			}
		}
	}
	return nil
}

// apiReachModel asks the local server, for a local model, whether it answers
// and has the model (agent.OpenAICompat.Probe, bounded by agent.ProbeTimeout).
func apiReachModel(ctx context.Context, a *App, model string) *Problem {
	provider, err := agent.APIModel(model)
	if err != nil || provider != agent.LocalProvider {
		return nil
	}
	c, err := a.Local()
	if err != nil || c.CheckURL() != nil {
		return nil // apiModelProblem says so
	}
	if err := c.Probe(ctx, localID(agent.LocalModel(model, ""))); err != nil {
		return &Problem{Text: "its model " + model + " needs the local server: " + err.Error()}
	}
	return nil
}

// onlyLocal is whether the routines run only models on the local server:
// then the claude-api backend needs no Anthropic API key. With no routines it
// does (the backend's usual way).
func onlyLocal(a *App) bool {
	set, err := routines.Load(a.Layout.Routines())
	if err != nil {
		return false
	}
	local := false
	for _, r := range set.Routines {
		switch provider, _ := agent.APIModel(r.Model); provider {
		case agent.AnthropicProvider:
			return false
		case agent.LocalProvider:
			local = true
		}
	}
	return local
}

// localID is a local/<model> model's ID on the server.
func localID(model string) string { return strings.TrimPrefix(model, agent.LocalProvider+"/") }
