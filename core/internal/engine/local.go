package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/secrets"
)

// LocalKey is the secret-store key of the local server's API key (optional:
// local servers need none).
const LocalKey = "local-api-key"

// formerLocalKey is where the local server's API key was stored before it
// was LocalKey: still read when LocalKey isn't stored.
const formerLocalKey = "openai-compat-api-key"

// LocalKeys are every secret-store key the local server's API key may be
// under, LocalKey first (`auth logout local` deletes them all).
var LocalKeys = []string{LocalKey, formerLocalKey}

// The local server's settings.
const (
	LocalURLSetting       = "llm.local.url"
	LocalModelSetting     = "llm.local.model"
	LocalKeepAliveSetting = "llm.local.keep-alive"
)

// Local is my own OpenAI-compatible server (the local backend's, and
// claude-api's local/<model> routines'): its URL from the settings, its API
// key when one is stored.
func (a *App) Local() (agent.OpenAICompat, error) {
	vals, err := a.Settings()
	if err != nil {
		return agent.OpenAICompat{}, err
	}
	key, err := localKey(a)
	if err != nil {
		return agent.OpenAICompat{}, fmt.Errorf("can't read the local server's API key: %w", err)
	}
	keepAlive := vals.String(LocalKeepAliveSetting)
	if keepAlive != "" {
		d, err := time.ParseDuration(keepAlive)
		if err != nil || d < 0 {
			return agent.OpenAICompat{}, fmt.Errorf("%s %q: want a duration like 1m (0 unloads after each request), or empty: `%s settings set %s 1m`",
				LocalKeepAliveSetting, keepAlive, product.Command, LocalKeepAliveSetting)
		}
		keepAlive = d.String()
	}
	return agent.OpenAICompat{BaseURL: vals.String(LocalURLSetting), APIKey: key, KeepAlive: keepAlive}, nil
}

// localKey is the local server's API key ("" when none is stored).
func localKey(a *App) (string, error) {
	for _, k := range LocalKeys {
		key, stored, err := secrets.Lookup(a.Connector.Secrets, k)
		if err != nil || stored {
			return key, err
		}
	}
	return "", nil
}

// localModel is the model routines run on with the local backend
// (llm.local.model), "" when none is set.
func (a *App) localModel() string {
	vals, err := a.Settings()
	if err != nil {
		return ""
	}
	return vals.String(LocalModelSetting)
}

// newLocal is the local backend: every routine on the local server.
func newLocal(a *App) (agent.Backend, error) {
	c, err := a.Local()
	if err != nil {
		return nil, err
	}
	return &agent.Genkit{Backend: agent.LocalBackend, Local: c, LocalModel: a.localModel()}, nil
}

// localReady says what keeps the local backend from running routines,
// without connecting: the server's URL isn't an http(s) URL, or no model is
// set. Its key is needed only when the server asks for one, which only
// connecting tells (doctor, routines check).
func localReady(a *App) (string, string) {
	c, err := a.Local()
	if err != nil {
		return err.Error(), ""
	}
	var missing []string
	setURL := ": `" + product.Command + " settings set " + LocalURLSetting + " <url>`"
	switch {
	case c.BaseURL == "":
		missing = append(missing, "no server URL is set"+setURL)
	case c.CheckURL() != nil:
		missing = append(missing, fmt.Sprintf("the server URL %q isn't an http(s) URL", c.BaseURL)+setURL)
	}
	if a.localModel() == "" {
		missing = append(missing, "no model is set: `"+product.Command+" settings set "+LocalModelSetting+
			" <model>` (its name on the server, e.g. qwen3:8b)")
	}
	return strings.Join(missing, ", and "), ""
}

// localServer is the local server, or why it can't be used, without
// connecting: its settings, or a URL that isn't http(s). The problem says
// before, the server, after, then why.
func (a *App) localServer(before, after string) (agent.OpenAICompat, *Problem) {
	c, err := a.Local()
	if err == nil {
		err = c.CheckURL()
	}
	if err != nil {
		return c, &Problem{
			Text: before + "the local server (experimental, untested)" + after + err.Error() + " (" + LocalURLSetting + ")",
			Fix:  product.Command + " settings set " + LocalURLSetting + " <url>",
		}
	}
	return c, nil
}

// reachLocal asks the local server whether it answers and has the model m
// (nil when it can't be asked: localServer says why). The problem says
// before, then why.
func (a *App) reachLocal(ctx context.Context, m, before string) *Problem {
	c, p := a.localServer("", "")
	if p != nil || m == "" {
		return nil
	}
	if err := c.Probe(ctx, localID(m)); err != nil {
		return &Problem{Text: before + err.Error()}
	}
	return nil
}

// localModelProblem is what keeps a routine from running on the local
// backend, without connecting: the server's URL, or no model to run it on
// (the routine names no local/<model>, and llm.local.model isn't set).
func localModelProblem(a *App, model string) *Problem {
	if _, p := a.localServer("", " can't be used: "); p != nil {
		return p
	}
	if agent.LocalModel(model, a.localModel()) == "" {
		return &Problem{
			Text: "the local backend has no model to run it on (" + LocalModelSetting + ")",
			Fix:  product.Command + " settings set " + LocalModelSetting + " <model>",
		}
	}
	return nil
}

// localReachModel asks the local server whether it answers and has the
// model the routine runs on.
func localReachModel(ctx context.Context, a *App, model string) *Problem {
	m := agent.LocalModel(model, a.localModel())
	return a.reachLocal(ctx, m, "it runs on "+m+", but ")
}
