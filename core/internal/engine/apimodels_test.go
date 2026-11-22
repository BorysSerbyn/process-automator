package engine

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/history"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/secrets"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
)

// writeRoutine writes a routine running model and returns it, loaded.
func writeRoutine(t *testing.T, a *App, name, model string) routines.Routine {
	t.Helper()
	dir := a.Layout.Routines()
	_ = os.MkdirAll(dir, 0o700)
	src := "---\nmodel: " + model + "\n---\nGo.\n"
	if model == "" {
		src = "Go.\n"
	}
	if err := os.WriteFile(filepath.Join(dir, name+".prompt"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	set, err := routines.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	r, err := set.Find(name)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func setSetting(t *testing.T, a *App, key, value string) {
	t.Helper()
	if _, err := (settings.Store{Path: a.Layout.Config()}).Set(key, value); err != nil {
		t.Fatal(err)
	}
}

func problemsOf(a *App, r routines.Routine) string {
	var out []string
	for _, p := range a.Assess(r).Problems {
		out = append(out, p.Text+" [fix: "+p.Fix+"]")
	}
	return strings.Join(out, "; ")
}

// On the api backend, an Anthropic API key is needed only when a routine
// runs an Anthropic model (or there are no routines yet, the usual way);
// each routine's model needs its own provider set up.
func TestAPIReadiness(t *testing.T) {
	a := testApp(t)
	if b, err := a.BackendReady(); err != nil || b.Problem != "no Anthropic API key is stored" || b.SignIn != "anthropic" {
		t.Errorf("no routines: %+v %v", b, err)
	}
	local := writeRoutine(t, a, "local", "openai-compat/qwen3:8b")
	if b, _ := a.BackendReady(); b.Problem != "" || b.Status() != "Runs with claude-api, ready" {
		t.Errorf("only an OpenAI-compatible model, no key: %+v", b)
	}
	if p := problemsOf(a, local); p != "" {
		t.Errorf("local: %s", p)
	}
	none := writeRoutine(t, a, "none", "")
	if b, _ := a.BackendReady(); b.Problem != "" {
		t.Errorf("a routine without a model asks for no key: %+v", b)
	}
	if p := problemsOf(a, none); p != "" {
		t.Errorf("no model: %s", p) // as before: its run fails, saying so
	}

	claude := writeRoutine(t, a, "claude", "anthropic/claude-sonnet-5-5")
	if b, _ := a.BackendReady(); b.Problem != "no Anthropic API key is stored" {
		t.Errorf("an Anthropic model, no key: %+v", b)
	}
	if p := problemsOf(a, claude); p != "its model anthropic/claude-sonnet-5-5 needs an Anthropic API key, and none is stored [fix: procauto auth login anthropic]" {
		t.Errorf("claude, no key: %s", p)
	}
	_ = a.Connector.Secrets.Set(AnthropicKey, "sk-ant-test")
	if b, _ := a.BackendReady(); b.Problem != "" {
		t.Errorf("with a key: %+v", b)
	}
	if p := problemsOf(a, claude) + problemsOf(a, local); p != "" {
		t.Errorf("with a key: %s", p)
	}

	setSetting(t, a, OpenAICompatURLSetting, "")
	if p := problemsOf(a, local); !strings.Contains(p, "its model openai-compat/qwen3:8b needs the OpenAI-compatible server (experimental, untested), but no URL is set") ||
		!strings.Contains(p, "fix: procauto settings set llm.openai-compat.base-url <url>") {
		t.Errorf("no URL: %s", p)
	}
	setSetting(t, a, OpenAICompatURLSetting, "localhost:11434")
	if p := problemsOf(a, local); !strings.Contains(p, `its URL "localhost:11434" isn't an http(s) URL`) {
		t.Errorf("not a URL: %s", p)
	}
	other := writeRoutine(t, a, "other", "openai/gpt-5.5")
	if p := problemsOf(a, other); !strings.Contains(p, `its model "openai/gpt-5.5" isn't one the claude-api backend runs`) {
		t.Errorf("another provider: %s", p)
	}

	// Only the api backend checks: claude-code has its own models.
	setSetting(t, a, "llm.backend", "claude-code")
	if p := problemsOf(a, local); p != "" {
		t.Errorf("on claude-code: %s", p)
	}
}

// fakeOllama serves an OpenAI-compatible API: its models, and a model that
// reports done, then answers.
func fakeOllama(t *testing.T, models ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/models" {
			var data []map[string]string
			for _, m := range models {
				data = append(data, map[string]string{"id": m})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
			return
		}
		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		msg := map[string]any{"role": "assistant", "content": "Done."}
		if req.Messages[len(req.Messages)-1].Role != "tool" {
			msg = map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{
				"id": "call_1", "type": "function",
				"function": map[string]any{"name": "report_outcome", "arguments": `{"outcome":"done","reason":"did it"}`},
			}}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "x", "object": "chat.completion", "model": req.Model,
			"choices": []map[string]any{{"index": 0, "message": msg, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 1000, "completion_tokens": 100, "total_tokens": 1100},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Doctor and routines check ask the server whether it answers and has the
// model; Assess (the scheduler's check) never connects.
func TestAPIReachModel(t *testing.T) {
	a := testApp(t)
	srv := fakeOllama(t, "qwen3:8b")
	setSetting(t, a, OpenAICompatURLSetting, srv.URL+"/v1")
	reach := func(r routines.Routine) string {
		rd := a.Assess(r)
		rd.CheckModel(context.Background(), a, r)
		var out []string
		for _, p := range rd.Problems {
			out = append(out, p.Text)
		}
		return strings.Join(out, "; ")
	}
	if p := reach(writeRoutine(t, a, "local", "openai-compat/qwen3:8b")); p != "" {
		t.Errorf("the server has it: %s", p)
	}
	missing := writeRoutine(t, a, "missing", "openai-compat/qwen3:32b")
	if p := reach(missing); !strings.HasPrefix(p, "its model openai-compat/qwen3:32b needs the OpenAI-compatible server: the server at "+srv.URL+"/v1 doesn't have the model qwen3:32b") {
		t.Errorf("not on the server: %s", p)
	}
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	down := "http://" + l.Addr().String() + "/v1"
	_ = l.Close()
	setSetting(t, a, OpenAICompatURLSetting, down)
	if p := reach(missing); p != "its model openai-compat/qwen3:32b needs the OpenAI-compatible server: nothing answers at "+down {
		t.Errorf("down: %s", p)
	}
	if p := problemsOf(a, missing); p != "" {
		t.Errorf("Assess connected: %s", p)
	}
	if p := reach(writeRoutine(t, a, "claude", "anthropic/claude-haiku-5-5")); strings.Contains(p, "OpenAI-compatible") {
		t.Errorf("an Anthropic model reached for the OpenAI-compatible server: %s", p)
	}
}

// A run on the OpenAI-compatible server has no list price: it's recorded
// without a cost, and the API budgets count only what Anthropic runs spent.
func TestOpenAICompatRunsArentPriced(t *testing.T) {
	a := testApp(t)
	srv := fakeOllama(t)
	setSetting(t, a, OpenAICompatURLSetting, srv.URL+"/v1")
	r := writeRoutine(t, a, "local", "openai-compat/qwen3:8b")
	set, err := routines.Load(a.Layout.Routines())
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.Execute(context.Background(), set, r, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Backend != "claude-api" || res.Outcome != "done" || res.InputTokens != 2000 || res.CostUSD != 0 {
		t.Errorf("result: %+v", res)
	}

	earlier := time.Now().Add(-time.Hour)
	if err := history.Save(a.Layout.History(), history.Entry{
		Routine: "claude", StartedAt: earlier, FinishedAt: &earlier, Outcome: "success",
		Result: &agent.Result{Backend: "api", CostUSD: 1.5},
	}); err != nil {
		t.Fatal(err)
	}
	u, err := a.Usage(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range u.Windows {
		if w.SpentUSD == nil || *w.SpentUSD != 1.5 {
			t.Errorf("%s window spent %v, want 1.5 (the Anthropic run only)", w.Name, w.SpentUSD)
		}
	}
}

// lockedStore can't be read, like a locked Keychain or keyring.
type lockedStore struct{ *secrets.Memory }

func (lockedStore) Get(string) (string, error) { return "", errors.New("the keychain is locked") }

// A secret that can't be read (a locked Keychain or keyring) is never taken
// for one that isn't stored: readiness, a routine's model, and runs say so.
func TestUnreadableSecrets(t *testing.T) {
	a := testApp(t)
	claude := writeRoutine(t, a, "claude", "anthropic/claude-sonnet-5-5")
	a.Connector.Secrets = lockedStore{secrets.NewMemory()}
	if b, _ := a.BackendReady(); !strings.HasPrefix(b.Problem, "the Anthropic API key can't be read: the keychain is locked") || b.SignIn != "" {
		t.Errorf("claude-api readiness: %+v", b)
	}
	if p := problemsOf(a, claude); !strings.Contains(p, "needs the Anthropic API key, which can't be read: the keychain is locked") {
		t.Errorf("claude's model: %s", p)
	}
	if _, err := Backends["claude-api"].New(a); err == nil || !strings.Contains(err.Error(), "can't read the Anthropic API key") {
		t.Errorf("claude-api run: %v", err)
	}
	if _, err := a.OpenAICompat(); err == nil || !strings.Contains(err.Error(), "can't read the OpenAI-compatible server's API key") {
		t.Errorf("openai-compat: %v", err)
	}
}
