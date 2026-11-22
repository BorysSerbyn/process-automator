package engine

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/usage"
)

// On the local backend, readiness needs no network: the server's URL is an
// http(s) URL and a model is set; each routine has a model to run on.
func TestLocalReadiness(t *testing.T) {
	a := testApp(t)
	setSetting(t, a, "llm.backend", "local")
	b, err := a.BackendReady()
	if err != nil || b.Problem != "no model is set: `procauto settings set llm.local.model <model>` (its name on the server, e.g. qwen3:8b)" ||
		!strings.HasPrefix(b.Status(), "Runs with local (experimental, untested), but no model is set") || b.SignIn != "" {
		t.Errorf("no model: %+v %v", b, err)
	}
	claude := writeRoutine(t, a, "claude", "anthropic/claude-sonnet-5-5")
	if p := problemsOf(a, claude); p != "the local backend has no model to run it on (llm.local.model) [fix: procauto settings set llm.local.model <model>]" {
		t.Errorf("a Claude routine, no model: %s", p)
	}
	own := writeRoutine(t, a, "own", "local/llama3.2:3b")
	if p := problemsOf(a, own); p != "" {
		t.Errorf("a routine naming its own model: %s", p)
	}

	setSetting(t, a, LocalURLSetting, "")
	if b, _ := a.BackendReady(); !strings.HasPrefix(b.Problem, "no server URL is set: `procauto settings set llm.local.url <url>`, and no model is set") {
		t.Errorf("no URL, no model: %+v", b)
	}
	if p := problemsOf(a, own); !strings.Contains(p, "the local server (experimental, untested) can't be used: no URL is set") {
		t.Errorf("no URL: %s", p)
	}
	setSetting(t, a, LocalURLSetting, "localhost:11434")
	setSetting(t, a, LocalModelSetting, "qwen3:8b")
	if b, _ := a.BackendReady(); b.Problem != `the server URL "localhost:11434" isn't an http(s) URL: `+"`procauto settings set llm.local.url <url>`" {
		t.Errorf("not a URL: %+v", b)
	}

	setSetting(t, a, LocalURLSetting, "http://localhost:11434/v1")
	if b, _ := a.BackendReady(); b.Problem != "" || b.Status() != "Runs with local (experimental, untested), ready" {
		t.Errorf("ready: %+v", b)
	}
	if p := problemsOf(a, claude) + problemsOf(a, own); p != "" {
		t.Errorf("ready: %s", p)
	}
	// No Anthropic key is ever asked for.
	if b, _ := a.BackendReady(); b.SignIn != "" {
		t.Errorf("sign-in: %+v", b)
	}
}

// Runs on the local backend go to the local server, on llm.local.model
// whatever model the routine names, or on the local/<model> it names; the
// server's key is sent only when one is stored (under its former name too).
// They have no cost, and the app has no usage limit for them.
func TestLocalRuns(t *testing.T) {
	a := testApp(t)
	srv, seen := fakeOllamaSeen(t, "qwen3:8b", "llama3.2:3b")
	setSetting(t, a, "llm.backend", "local")
	setSetting(t, a, LocalURLSetting, srv.URL+"/v1")
	setSetting(t, a, LocalModelSetting, "qwen3:8b")
	claude := writeRoutine(t, a, "claude", "anthropic/claude-sonnet-5-5")
	own := writeRoutine(t, a, "own", "local/llama3.2:3b")
	run := func(r routines.Routine) {
		t.Helper()
		set, err := routines.Load(a.Layout.Routines())
		if err != nil {
			t.Fatal(err)
		}
		res, err := a.Execute(context.Background(), set, r, RunOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if res.Backend != "local" || res.Outcome != "done" || res.CostUSD != 0 {
			t.Errorf("result: %+v", res)
		}
	}
	run(claude)
	run(own)
	got := seen()
	if len(got) != 4 || got[0].model != "qwen3:8b" || got[2].model != "llama3.2:3b" || got[0].auth != "" {
		t.Errorf("requests: %+v", got)
	}

	_ = a.Connector.Secrets.Set(formerLocalKey, "sk-former")
	run(claude)
	if got := seen(); got[len(got)-1].auth != "Bearer sk-former" {
		t.Errorf("the key under its former name wasn't sent: %+v", got[len(got)-1])
	}
	_ = a.Connector.Secrets.Set(LocalKey, "sk-local")
	run(claude)
	if got := seen(); got[len(got)-1].auth != "Bearer sk-local" {
		t.Errorf("the key wasn't sent: %+v", got[len(got)-1])
	}

	if m, err := Backends["local"].AgentModel(a, claude.Model); err != nil || m != "local/qwen3:8b" {
		t.Errorf("agent model: %q %v", m, err)
	}
	if m, _ := Backends["local"].AgentModel(a, own.Model); m != "local/llama3.2:3b" {
		t.Errorf("agent model of local/: %q", m)
	}

	u, err := a.Usage(context.Background(), time.Now())
	if err != nil || u.Kind != usage.KindNone || len(u.Windows) != 0 || u.Over {
		t.Errorf("usage: %+v %v", u, err)
	}
}

// Doctor and routines check ask the local server whether it answers and has
// the model the routine runs on; Assess never connects.
func TestLocalReachModel(t *testing.T) {
	a := testApp(t)
	srv := fakeOllama(t, "qwen3:8b")
	setSetting(t, a, "llm.backend", "local")
	setSetting(t, a, LocalURLSetting, srv.URL+"/v1")
	setSetting(t, a, LocalModelSetting, "qwen3:8b")
	reach := func(r routines.Routine) string {
		rd := a.Assess(a.LLM(r, nil))
		rd.CheckModel(context.Background(), a, r)
		var out []string
		for _, p := range rd.Problems {
			out = append(out, p.Text)
		}
		return strings.Join(out, "; ")
	}
	claude := writeRoutine(t, a, "claude", "anthropic/claude-sonnet-5-5")
	if p := reach(claude); p != "" {
		t.Errorf("the server has it: %s", p)
	}
	setSetting(t, a, LocalModelSetting, "qwen3:32b")
	if p := reach(claude); !strings.HasPrefix(p, "it runs on local/qwen3:32b, but the server at "+srv.URL+"/v1 doesn't have the model qwen3:32b") ||
		!strings.Contains(p, "ollama pull qwen3:32b") {
		t.Errorf("not pulled: %s", p)
	}
	if p := problemsOf(a, claude); p != "" {
		t.Errorf("Assess connected: %s", p)
	}
}

// The local server's URL setting was llm.openai-compat.base-url: a config
// that still has it is read as llm.local.url.
func TestLocalFormerURLSetting(t *testing.T) {
	a := testApp(t)
	if err := os.WriteFile(a.Layout.Config(), []byte("settings:\n  llm.openai-compat.base-url: http://gpu-box:8080/v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := a.Local()
	if err != nil || c.BaseURL != "http://gpu-box:8080/v1" {
		t.Errorf("former URL setting: %+v %v", c, err)
	}
}

// The local server gets llm.local.keep-alive with each request: 1m by
// default, as a Go duration; empty sends none; anything else that isn't a
// duration keeps the local backend from running, saying how to fix it.
func TestLocalKeepAlive(t *testing.T) {
	a := testApp(t)
	setSetting(t, a, "llm.backend", "local")
	setSetting(t, a, "llm.local.model", "qwen3:8b")
	keepAlive := func() string {
		c, err := a.Local()
		if err != nil {
			t.Fatal(err)
		}
		return c.KeepAlive
	}
	if got := keepAlive(); got != "1m0s" {
		t.Errorf("default: %q", got)
	}
	setSetting(t, a, "llm.local.keep-alive", "0")
	if got := keepAlive(); got != "0s" {
		t.Errorf("0: %q", got)
	}
	setSetting(t, a, "llm.local.keep-alive", "")
	if got := keepAlive(); got != "" {
		t.Errorf("empty: %q", got)
	}
	setSetting(t, a, "llm.local.keep-alive", "soon")
	if b, _ := a.BackendReady(); !strings.Contains(b.Problem, `llm.local.keep-alive "soon": want a duration like 1m`) {
		t.Errorf("invalid: %+v", b)
	}
}
