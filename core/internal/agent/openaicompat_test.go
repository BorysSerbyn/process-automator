package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// toolCall is a tool call a fake model turn makes.
type toolCall struct {
	name string
	args map[string]any
}

// fakeOpenAI is an OpenAI-compatible server (Chat Completions and the models
// list) whose model calls tools turn by turn, then answers.
type fakeOpenAI struct {
	t     *testing.T
	turns [][]toolCall // each turn's tool calls; past the last, it answers
	// fail, if set, is the status every chat completion answers with.
	fail   int
	models []string
	// refuseKeepAlive answers a request carrying keep_alive with a 400, as
	// OpenAI's API does an argument it doesn't know.
	refuseKeepAlive bool
	refused         int // requests refused for keep_alive (not turns)
	// badRequest answers every request with a 400 of this message.
	badRequest string

	mu       sync.Mutex
	requests []chatRequest
	auth     []string // each request's Authorization header
}

type chatRequest struct {
	Model     string          `json:"model"`
	KeepAlive json.RawMessage `json:"keep_alive"`
	Messages  []struct {
		Role       string `json:"role"`
		Content    any    `json:"content"`
		ToolCallID string `json:"tool_call_id"`
		ToolCalls  []struct {
			ID       string `json:"id"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	} `json:"messages"`
	Tools []struct {
		Type     string `json:"type"`
		Function struct {
			Name       string         `json:"name"`
			Parameters map[string]any `json:"parameters"`
		} `json:"function"`
	} `json:"tools"`
}

func (f *fakeOpenAI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
		data := []map[string]any{}
		for _, m := range f.models {
			data = append(data, map[string]any{"id": m, "object": "model"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
		return
	case r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions":
		http.NotFound(w, r)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var req chatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		f.t.Errorf("request: %v: %s", err, body)
	}
	if f.refuseKeepAlive && req.KeepAlive != nil {
		f.refused++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"Unrecognized request argument supplied: keep_alive","type":"invalid_request_error"}}`)
		return
	}
	f.requests = append(f.requests, req)
	if f.badRequest != "" {
		w.Header().Set("X-Should-Retry", "false")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"`+f.badRequest+`","type":"invalid_request_error"}}`)
		return
	}
	if f.fail != 0 {
		w.Header().Set("X-Should-Retry", "false") // the SDK would retry it
		w.WriteHeader(f.fail)
		_, _ = io.WriteString(w, `{"error":{"message":"model runner has crashed","type":"server_error"}}`)
		return
	}
	turn := len(f.requests) - 1
	msg := map[string]any{"role": "assistant", "content": "All posted."}
	finish := "stop"
	if turn < len(f.turns) {
		var calls []map[string]any
		for i, c := range f.turns[turn] {
			args, _ := json.Marshal(c.args)
			calls = append(calls, map[string]any{
				"id": "call_" + string(rune('a'+turn)) + string(rune('0'+i)), "type": "function",
				"function": map[string]any{"name": c.name, "arguments": string(args)},
			})
		}
		msg = map[string]any{"role": "assistant", "content": "", "tool_calls": calls}
		finish = "tool_calls"
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "chatcmpl-1", "object": "chat.completion", "created": 1, "model": req.Model,
		"choices": []map[string]any{{"index": 0, "message": msg, "finish_reason": finish}},
		"usage":   map[string]any{"prompt_tokens": 100, "completion_tokens": 20, "total_tokens": 120},
	})
}

const compatRoutine = "---\nmodel: local/qwen3:1.7b\n---\n{{role \"system\"}}You post updates.\n{{role \"user\"}}Post today's update."

// runCompat runs compatRoutine with the chat plugin's tools on the fake.
func runCompat(t *testing.T, f *fakeOpenAI, key string, dryRun bool) (*Result, error) {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	ag := testAgent(0, dryRun, nil)
	ag.Backend = &Genkit{Local: OpenAICompat{BaseURL: srv.URL + "/v1", APIKey: key}}
	return ag.Run(context.Background(), Request{Name: "r", Source: compatRoutine, Servers: []string{"chat"}, Tools: []string{"read", "post"}})
}

func reportCall(outcome string) toolCall {
	return toolCall{OutcomeTool, map[string]any{"outcome": outcome, "reason": "posted the update"}}
}

func TestOpenAICompatRunCallsToolsThroughTheGateway(t *testing.T) {
	f := &fakeOpenAI{t: t, turns: [][]toolCall{
		{{"read", map[string]any{}}},
		{{"post", map[string]any{}}, reportCall(OutcomeDone)},
	}}
	res, err := runCompat(t, f, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "All posted." || res.Outcome != OutcomeDone || res.Backend != "claude-api" {
		t.Errorf("result: %+v", res)
	}
	if len(res.Calls) != 2 || res.Calls[0].Tool != "read" || res.Calls[1].Tool != "post" || !res.Calls[1].Write || len(res.Writes) != 1 {
		t.Errorf("calls: %+v, writes: %+v", res.Calls, res.Writes)
	}
	// Tokens add up over the three model calls; the cost is unknown (no list price).
	if res.InputTokens != 300 || res.OutputTokens != 60 || res.CostUSD != 0 {
		t.Errorf("usage: %d in, %d out, $%v", res.InputTokens, res.OutputTokens, res.CostUSD)
	}

	if len(f.requests) != 3 {
		t.Fatalf("%d model calls, want 3", len(f.requests))
	}
	first := f.requests[0]
	if first.Model != "qwen3:1.7b" {
		t.Errorf("model sent: %q", first.Model)
	}
	var tools []string
	for _, tool := range first.Tools {
		if tool.Type != "function" || tool.Function.Parameters["type"] != "object" {
			t.Errorf("tool: %+v", tool)
		}
		tools = append(tools, tool.Function.Name)
	}
	if strings.Join(tools, ",") != "post,read,report_outcome" {
		t.Errorf("tools offered: %v", tools)
	}
	if sys := first.Messages[0]; sys.Role != "system" || !strings.Contains(sys.Content.(string), OutcomeTool) {
		t.Errorf("system message: %+v", sys)
	}
	// The second call carries the model's tool call and the tool's result, matched by ID.
	second := f.requests[1].Messages
	var call, result bool
	for _, m := range second {
		if m.Role == "assistant" && len(m.ToolCalls) == 1 && m.ToolCalls[0].ID == "call_a0" && m.ToolCalls[0].Function.Name == "read" {
			call = true
		}
		if m.Role == "tool" && m.ToolCallID == "call_a0" && strings.Contains(m.Content.(string), "hello") {
			result = true
		}
	}
	if !call || !result {
		t.Errorf("the tool call and its result weren't sent back: %+v", second)
	}
	third := f.requests[2].Messages
	if last := third[len(third)-1]; last.Role != "tool" || last.ToolCallID != "call_b1" || !strings.Contains(last.Content.(string), "Recorded") {
		t.Errorf("last message of the third call: %+v", last)
	}
}

func TestOpenAICompatDryRunInterceptsWrites(t *testing.T) {
	f := &fakeOpenAI{t: t, turns: [][]toolCall{{{"post", map[string]any{}}, reportCall(OutcomeDone)}}}
	res, err := runCompat(t, f, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.DryRun || len(res.Writes) != 1 || res.Writes[0].Tool != "post" {
		t.Errorf("writes: %+v", res.Writes)
	}
	var sent bool
	for _, m := range f.requests[1].Messages {
		if m.Role == "tool" && strings.Contains(m.Content.(string), "DRY RUN") {
			sent = true
		}
	}
	if !sent {
		t.Error("the model wasn't told the write was intercepted")
	}
}

func TestOpenAICompatWithoutAReportFails(t *testing.T) {
	f := &fakeOpenAI{t: t, turns: [][]toolCall{{{"read", map[string]any{}}}}}
	_, err := runCompat(t, f, "", false)
	var failed *RoutineFailedError
	if !errors.As(err, &failed) || !strings.Contains(err.Error(), "without reporting an outcome") {
		t.Errorf("err = %v", err)
	}
}

func TestOpenAICompatAPIKey(t *testing.T) {
	f := &fakeOpenAI{t: t, turns: [][]toolCall{{reportCall(OutcomeDone)}}}
	if _, err := runCompat(t, f, "sk-test", false); err != nil {
		t.Fatal(err)
	}
	for _, h := range f.auth {
		if h != "Bearer sk-test" {
			t.Errorf("Authorization = %q", h)
		}
	}

	t.Setenv("OPENAI_API_KEY", "sk-from-the-environment") // never sent elsewhere
	f = &fakeOpenAI{t: t, turns: [][]toolCall{{reportCall(OutcomeDone)}}}
	if _, err := runCompat(t, f, "", false); err != nil {
		t.Fatal(err)
	}
	for _, h := range f.auth {
		if h != "" {
			t.Errorf("Authorization without a key = %q", h)
		}
	}
}

func TestOpenAICompatServerError(t *testing.T) {
	f := &fakeOpenAI{t: t, fail: http.StatusInternalServerError}
	_, err := runCompat(t, f, "", false)
	if err == nil || !strings.Contains(err.Error(), "answered with an error (HTTP 500): model runner has crashed") ||
		!strings.Contains(err.Error(), "local server at http://127.0.0.1:") {
		t.Errorf("err = %v", err)
	}
}

func TestOpenAICompatUnreachable(t *testing.T) {
	url := closedURL(t)
	ag := testAgent(0, false, nil)
	ag.Backend = &Genkit{Local: OpenAICompat{BaseURL: url + "/v1"}}
	_, err := ag.Run(context.Background(), Request{Name: "r", Source: compatRoutine, Servers: []string{"chat"}, Tools: []string{"read"}})
	if err == nil || !strings.Contains(err.Error(), "the local server at "+url+"/v1 isn't answering") {
		t.Errorf("err = %v", err)
	}
}

// closedURL is the URL of a port nothing listens on.
func closedURL(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + l.Addr().String()
	_ = l.Close()
	return url
}

func TestGenkitRoutesByProvider(t *testing.T) {
	src := func(model string) string {
		return "---\nmodel: " + model + "\n---\nGo."
	}
	run := func(b *Genkit, model string) error {
		_, err := b.Run(context.Background(), Job{Name: "r", Source: src(model), MaxTurns: 3})
		return err
	}
	for model, want := range map[string]string{
		"anthropic/claude-sonnet-5-5": "needs an Anthropic API key, and none is stored",
		"local/qwen3":                 "needs the local server's URL",
		"openai-compat/qwen3":         "needs the local server's URL",
		"openai/gpt-5.5":              "isn't one the claude-api backend runs",
		"claude-sonnet-5-5":           "names no provider",
	} {
		if err := run(&Genkit{}, model); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", model, err, want)
		}
	}
	if err := run(&Genkit{}, ""); err == nil || !strings.Contains(err.Error(), "names no model") {
		t.Errorf("no model: %v", err)
	}
	// An Anthropic model goes to the Anthropic API, even with the local
	// server set up.
	anthropicAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer anthropicAPI.Close()
	t.Setenv("ANTHROPIC_BASE_URL", anthropicAPI.URL)
	compat := &fakeOpenAI{t: t}
	srv := httptest.NewServer(compat)
	defer srv.Close()
	b := &Genkit{APIKey: "sk-ant-test", Local: OpenAICompat{BaseURL: srv.URL + "/v1"}}
	if err := run(b, "anthropic/claude-sonnet-5-5"); err == nil || !strings.Contains(err.Error(), "418") || len(compat.requests) != 0 {
		t.Errorf("anthropic model: %v, %d requests to the OpenAI-compatible server", err, len(compat.requests))
	}
	if err := run(b, "local/qwen3"); err != nil || len(compat.requests) != 1 || compat.requests[0].Model != "qwen3" {
		t.Errorf("local model: %v, %+v", err, compat.requests)
	}
	// openai-compat/<model> is local/<model>'s former name.
	if err := run(b, "openai-compat/qwen3:8b"); err != nil || len(compat.requests) != 2 || compat.requests[1].Model != "qwen3:8b" {
		t.Errorf("openai-compat model: %v, %+v", err, compat.requests)
	}
}

func TestOpenAICompatProbe(t *testing.T) {
	f := &fakeOpenAI{t: t, models: []string{"qwen3:latest", "llama3.2:3b"}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := OpenAICompat{BaseURL: srv.URL + "/v1"}
	for model, want := range map[string]string{
		"qwen3":        "",
		"llama3.2:3b":  "",
		"qwen3:32b":    "doesn't have the model qwen3:32b (it has qwen3:latest, llama3.2:3b; on Ollama, `ollama pull qwen3:32b`)",
		"deepseek-r1:": "doesn't have the model",
	} {
		err := c.Probe(context.Background(), model)
		if (want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), want)) {
			t.Errorf("%s: %v, want %q", model, err, want)
		}
	}
	if err := (OpenAICompat{BaseURL: srv.URL}).Probe(context.Background(), "qwen3"); err == nil || !strings.Contains(err.Error(), "usually ends in /v1") {
		t.Errorf("without /v1: %v", err)
	}
	if err := (OpenAICompat{BaseURL: closedURL(t) + "/v1"}).Probe(context.Background(), "qwen3"); err == nil || !strings.Contains(err.Error(), "nothing answers at") {
		t.Errorf("unreachable: %v", err)
	}
	if got := (OpenAICompat{BaseURL: "http://localhost:11434/v1"}).hint(); got != " (is Ollama running?)" {
		t.Errorf("Ollama's hint: %q", got)
	}

	keyed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer right" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"deepseek-chat"}]}`)
	}))
	defer keyed.Close()
	for key, want := range map[string]string{
		"":      "needs an API key (HTTP 401): `procauto auth login local`",
		"wrong": "refuses its API key (HTTP 401)",
		"right": "",
	} {
		err := (OpenAICompat{BaseURL: keyed.URL, APIKey: key}).Probe(context.Background(), "deepseek-chat")
		if (want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), want)) {
			t.Errorf("key %q: %v, want %q", key, err, want)
		}
	}
	if err := (OpenAICompat{BaseURL: "localhost:11434"}).CheckURL(); err == nil {
		t.Error("a URL without a scheme was taken")
	}
}

func TestOpenAICompatToolErrorsGoBackToTheModel(t *testing.T) {
	// Input the tool's schema refuses, and a tool that doesn't exist: the
	// model is told, and carries on.
	f := &fakeOpenAI{t: t, turns: [][]toolCall{
		{{"post", map[string]any{"text": "hi"}}, {"delete_everything", map[string]any{}}},
		{{"post", map[string]any{}}, reportCall(OutcomeDone)},
	}}
	res, err := runCompat(t, f, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != OutcomeDone || len(res.Writes) != 1 {
		t.Errorf("result: %+v", res)
	}
	var told int
	for _, m := range f.requests[1].Messages {
		if s, _ := m.Content.(string); m.Role == "tool" && strings.Contains(s, "error") {
			told++
		}
	}
	if told != 2 {
		t.Errorf("%d tool errors sent back, want 2: %+v", told, f.requests[1].Messages)
	}
}

// The local backend runs every routine on its model (llm.local.model),
// whatever model the routine names, unless it names local/<model>; it sends
// the server's key only when one is stored.
func TestLocalBackendRunsItsModel(t *testing.T) {
	f := &fakeOpenAI{t: t}
	srv := httptest.NewServer(f)
	defer srv.Close()
	run := func(b *Genkit, model string) error {
		src := "---\nmodel: " + model + "\n---\nGo."
		if model == "" {
			src = "Go."
		}
		_, err := b.Run(context.Background(), Job{Name: "r", Source: src, MaxTurns: 3})
		return err
	}
	b := &Genkit{Backend: LocalBackend, Local: OpenAICompat{BaseURL: srv.URL + "/v1"}, LocalModel: "qwen3:8b"}
	if b.Name() != "local" {
		t.Errorf("name %q", b.Name())
	}
	for model, want := range map[string]string{
		"anthropic/claude-sonnet-5-5": "qwen3:8b",
		"":                            "qwen3:8b",
		"openai/gpt-5.5":              "qwen3:8b",
		"local/llama3.2:3b":           "llama3.2:3b",
		"openai-compat/llama3.2:3b":   "llama3.2:3b",
	} {
		f.requests = nil
		if err := run(b, model); err != nil || len(f.requests) != 1 || f.requests[0].Model != want {
			t.Errorf("%q: %v, %+v, want %s", model, err, f.requests, want)
		}
	}
	for _, h := range f.auth {
		if h != "" {
			t.Errorf("Authorization without a key = %q", h)
		}
	}
	f.auth = nil
	keyed := *b
	keyed.Local.APIKey = "sk-local"
	if err := run(&keyed, "anthropic/claude-haiku-5-5"); err != nil || len(f.auth) != 1 || f.auth[0] != "Bearer sk-local" {
		t.Errorf("with a key: %v, %q", err, f.auth)
	}

	none := &Genkit{Backend: LocalBackend, Local: OpenAICompat{BaseURL: srv.URL + "/v1"}}
	if err := run(none, "anthropic/claude-sonnet-5-5"); err == nil || !strings.Contains(err.Error(), "settings set llm.local.model <model>") {
		t.Errorf("no model set: %v", err)
	}
	if err := run(none, "local/qwen3"); err != nil {
		t.Errorf("no model set, a routine naming its own: %v", err)
	}
	if err := run(&Genkit{Backend: LocalBackend, LocalModel: "qwen3"}, ""); err == nil || !strings.Contains(err.Error(), "settings set llm.local.url <url>") {
		t.Errorf("no URL: %v", err)
	}
}

func runKeepAlive(t *testing.T, f *fakeOpenAI, keepAlive string) (*Result, error) {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	ag := testAgent(0, false, nil)
	ag.Backend = &Genkit{Local: OpenAICompat{BaseURL: srv.URL + "/v1", KeepAlive: keepAlive}}
	return ag.Run(context.Background(), Request{Name: "r", Source: compatRoutine, Servers: []string{"chat"}, Tools: []string{"read", "post"}})
}

// Every chat request of a run carries keep_alive (Ollama's extension), so the
// server frees the model soon after the run's last request.
func TestLocalKeepAliveOnEveryRequest(t *testing.T) {
	f := &fakeOpenAI{t: t, turns: [][]toolCall{{{"read", map[string]any{}}}, {reportCall("done")}}}
	if _, err := runKeepAlive(t, f, "1m0s"); err != nil {
		t.Fatal(err)
	}
	if len(f.requests) < 3 {
		t.Fatalf("a multi-turn run made %d requests", len(f.requests))
	}
	for i, r := range f.requests {
		if string(r.KeepAlive) != `"1m0s"` {
			t.Errorf("request %d: keep_alive %s", i, r.KeepAlive)
		}
	}
}

// Without a keep-alive set, nothing is added: the server keeps its own.
func TestLocalKeepAliveEmpty(t *testing.T) {
	f := &fakeOpenAI{t: t, turns: [][]toolCall{{reportCall("done")}}}
	if _, err := runKeepAlive(t, f, ""); err != nil {
		t.Fatal(err)
	}
	for i, r := range f.requests {
		if r.KeepAlive != nil {
			t.Errorf("request %d: keep_alive %s sent", i, r.KeepAlive)
		}
	}
}

// A server that refuses keep_alive gets the request again without it, and
// never again: the run goes on as if it hadn't been asked.
func TestLocalKeepAliveRefused(t *testing.T) {
	f := &fakeOpenAI{t: t, refuseKeepAlive: true, turns: [][]toolCall{{{"read", map[string]any{}}}, {reportCall("done")}}}
	if _, err := runKeepAlive(t, f, "1m0s"); err != nil {
		t.Fatal(err)
	}
	if f.refused != 1 || len(f.requests) < 3 {
		t.Fatalf("refused %d, then %d requests", f.refused, len(f.requests))
	}
	for i, r := range f.requests {
		if r.KeepAlive != nil {
			t.Errorf("request %d after the refusal: keep_alive %s", i, r.KeepAlive)
		}
	}
}

// Any other 400 isn't taken for a refusal: it fails the run, unretried.
func TestLocalKeepAliveOtherBadRequest(t *testing.T) {
	f := &fakeOpenAI{t: t, badRequest: "context length exceeded"}
	_, err := runKeepAlive(t, f, "1m0s")
	if err == nil || !strings.Contains(err.Error(), "context length exceeded") {
		t.Fatalf("err = %v", err)
	}
	if len(f.requests) != 1 {
		t.Errorf("a 400 was retried: %d requests", len(f.requests))
	}
}
