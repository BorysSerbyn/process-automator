package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"github.com/BorysSerbyn/process-automator/core/internal/product"
)

// OpenAICompat is an OpenAI-compatible server (its Chat Completions API):
// Ollama, llama.cpp's llama-server, vLLM, LM Studio, or a hosted API such as
// DeepSeek, Qwen (DashScope) or Kimi (Moonshot). Experimental, untested.
type OpenAICompat struct {
	// BaseURL is where its API is, e.g. http://localhost:11434/v1 (Ollama).
	BaseURL string
	// APIKey is sent as a bearer token; "" sends none (local servers need none).
	APIKey string
	// KeepAlive, when set (a duration like "1m"; "0s" unloads after each
	// request), is sent with every chat request as keep_alive: Ollama's
	// extension to the OpenAI API for how long it keeps the model loaded
	// after the last request from anyone. Other clients' requests (a chat
	// app) keep it loaded with their own. Servers that ignore unknown fields
	// ignore it; one that refuses it gets the request again without it.
	KeepAlive string
}

// keepAliveRefused are the servers (by base URL) that refused keep_alive in
// this process: it isn't sent to them again.
var keepAliveRefused sync.Map

// requestOptions are the OpenAI client's options for this server.
func (c OpenAICompat) requestOptions() []option.RequestOption {
	if c.KeepAlive == "" {
		return nil
	}
	return []option.RequestOption{option.WithMiddleware(c.addKeepAlive)}
}

// addKeepAlive adds keep_alive to a chat request's JSON body. A 400 that
// refuses the field sends the request again as it was, and the field isn't
// sent to that server again.
func (c OpenAICompat) addKeepAlive(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
	if !strings.HasSuffix(req.URL.Path, "/chat/completions") || req.Body == nil {
		return next(req)
	}
	if _, refused := keepAliveRefused.Load(c.BaseURL); refused {
		return next(req)
	}
	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil {
		return next(withBody(req, body))
	}
	fields["keep_alive"], _ = json.Marshal(c.KeepAlive)
	with, err := json.Marshal(fields)
	if err != nil {
		return next(withBody(req, body))
	}
	resp, err := next(withBody(req, with))
	if err != nil || resp.StatusCode != http.StatusBadRequest {
		return resp, err
	}
	answer, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !refusesKeepAlive(answer) {
		resp.Body = io.NopCloser(bytes.NewReader(answer))
		return resp, nil
	}
	keepAliveRefused.Store(c.BaseURL, true)
	return next(withBody(req, body))
}

// refusesKeepAlive says a 400's body refuses the keep_alive field (by name,
// or as an argument or field the server doesn't know).
func refusesKeepAlive(body []byte) bool {
	s := strings.ToLower(string(body))
	if strings.Contains(s, "keep_alive") {
		return true
	}
	for _, phrase := range []string{"unrecognized request argument", "unknown field", "unknown parameter", "extra fields not permitted", "extra inputs are not permitted"} {
		if strings.Contains(s, phrase) {
			return true
		}
	}
	return false
}

// withBody is req with body as its body.
func withBody(req *http.Request, body []byte) *http.Request {
	r := req.Clone(req.Context())
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	r.ContentLength = int64(len(body))
	r.Header.Set("Content-Length", strconv.Itoa(len(body)))
	return r
}

// CheckURL says what's wrong with the server's URL, without connecting.
func (c OpenAICompat) CheckURL() error {
	if c.BaseURL == "" {
		return errors.New("no URL is set")
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("its URL %q isn't an http(s) URL", c.BaseURL)
	}
	return nil
}

// ProbeTimeout bounds asking the server for its models (doctor, routines
// check, setup).
const ProbeTimeout = 3 * time.Second

// NeedsKeyError is a server that refuses to list its models without an API
// key, or with the one it got (HTTP 401 or 403).
type NeedsKeyError struct {
	BaseURL string
	Status  int
	// Refused is true when a key was sent.
	Refused bool
}

func (e *NeedsKeyError) Error() string {
	if e.Refused {
		return fmt.Sprintf("the server at %s refuses its API key (HTTP %d): `%s auth login %s` with another", e.BaseURL, e.Status, product.Command, LocalProvider)
	}
	return fmt.Sprintf("the server at %s needs an API key (HTTP %d): `%s auth login %s`", e.BaseURL, e.Status, product.Command, LocalProvider)
}

// Models asks the server for its models (GET <base URL>/models, which every
// OpenAI-compatible server here serves) and returns their IDs, or says in
// plain words why it can't: the server isn't answering, wants a key
// (*NeedsKeyError), or has no OpenAI-compatible API there. A server that
// answers in a shape of its own returns no IDs and ok false.
func (c OpenAICompat) Models(ctx context.Context) (ids []string, ok bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(c.BaseURL, "/")+"/models", nil)
	if err != nil {
		return nil, false, err
	}
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("nothing answers at %s%s", c.BaseURL, c.hint())
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, false, &NeedsKeyError{BaseURL: c.BaseURL, Status: resp.StatusCode, Refused: c.APIKey != ""}
	case resp.StatusCode == http.StatusNotFound:
		return nil, false, fmt.Errorf("the server at %s has no OpenAI-compatible API there (HTTP 404 for /models): check llm.local.url (it usually ends in /v1)", c.BaseURL)
	case resp.StatusCode/100 != 2:
		return nil, false, fmt.Errorf("the server at %s answered with an error (HTTP %d)", c.BaseURL, resp.StatusCode)
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &list) != nil || list.Data == nil {
		return nil, false, nil // answers, in a shape of its own: a run tells
	}
	ids = []string{}
	for _, m := range list.Data {
		ids = append(ids, m.ID)
	}
	return ids, true, nil
}

// Probe asks the server for its models (Models) and says, in plain words,
// why model (its ID on the server) can't run there: the server isn't
// answering, refuses the key, or doesn't list the model. nil when it can, as
// far as that tells.
func (c OpenAICompat) Probe(ctx context.Context, model string) error {
	ids, ok, err := c.Models(ctx)
	if err != nil || !ok {
		return err
	}
	// Ollama takes a model without a tag as its :latest.
	if slices.Contains(ids, model) || (!strings.Contains(model, ":") && slices.Contains(ids, model+":latest")) {
		return nil
	}
	has := "it has none"
	if len(ids) > 0 {
		has = "it has " + strings.Join(ids, ", ")
	}
	return fmt.Errorf("the server at %s doesn't have the model %s (%s; on Ollama, `ollama pull %s`)", c.BaseURL, model, has, model)
}

// plainError says what went wrong with the server in plain words: it isn't
// answering, or it answered with an error. Other errors are returned as they are.
func (c OpenAICompat) plainError(err error) error {
	var apiErr *openai.Error
	var netErr net.Error
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.As(err, &apiErr):
		msg := strings.TrimSpace(apiErr.Message)
		if msg == "" {
			msg = strings.TrimSpace(apiErr.RawJSON())
		}
		if msg == "" {
			msg = "no details"
		}
		return fmt.Errorf("the local server at %s answered with an error (HTTP %d): %s", c.BaseURL, apiErr.StatusCode, msg)
	case errors.As(err, &netErr):
		return fmt.Errorf("the local server at %s isn't answering%s: %w", c.BaseURL, c.hint(), err)
	}
	return err // not the server's (max turns, a tool)
}

// hint names Ollama when the URL is its default one.
func (c OpenAICompat) hint() string {
	if strings.HasPrefix(c.BaseURL, "http://localhost:11434") || strings.HasPrefix(c.BaseURL, "http://127.0.0.1:11434") {
		return " (is Ollama running?)"
	}
	return ""
}
