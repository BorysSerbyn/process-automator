package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BorysSerbyn/process-automator/core/internal/secrets"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// testServer is an MCP server that only answers requests carrying wantHeader.
func testServer(t *testing.T, sse bool, wantKey, wantValue string) string {
	t.Helper()
	srv := sdk.NewServer(&sdk.Implementation{Name: "t"}, nil)
	sdk.AddTool(srv, &sdk.Tool{Name: "ping"}, func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "pong"}}}, nil, nil
	})
	var h http.Handler
	if sse {
		h = sdk.NewSSEHandler(func(*http.Request) *sdk.Server { return srv }, nil)
	} else {
		h = sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv }, nil)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if wantKey != "" && r.Header.Get(wantKey) != wantValue {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func connectAndPing(t *testing.T, spec pluginapi.ServerSpec, store secrets.Store) error {
	t.Helper()
	if err := spec.Validate("t"); err != nil {
		t.Fatal(err)
	}
	c := &Connector{Name: "test", Servers: map[string]pluginapi.ServerSpec{"t": spec}, Secrets: store}
	s, err := c.Connect(context.Background(), "t")
	if err != nil {
		return err
	}
	defer s.Close()
	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: "ping"})
	if err != nil {
		return err
	}
	if got := pluginapi.ResultText(res); got != "pong" {
		t.Fatalf("ping = %q", got)
	}
	return nil
}

func TestRemoteAPIKeyHeader(t *testing.T) {
	url := testServer(t, false, "Authorization", "Bearer sk-1")
	spec := pluginapi.ServerSpec{URL: url, Auth: pluginapi.AuthHeaders, Headers: map[string]string{"Authorization": "Bearer {t-key}"}}

	store := secrets.NewMemory()
	if err := connectAndPing(t, spec, store); !errors.Is(err, ErrNeedsReauth) {
		t.Fatalf("missing key: want needs-reauth, got %v", err)
	}
	_ = store.Set("t-key", "sk-1")
	if err := connectAndPing(t, spec, store); err != nil {
		t.Fatalf("with key: %v", err)
	}
}

func TestRemotePublicServer(t *testing.T) {
	url := testServer(t, false, "X-Workspace", "acme")
	spec := pluginapi.ServerSpec{URL: url, Auth: pluginapi.AuthNone, Headers: map[string]string{"X-Workspace": "acme"}}
	if err := connectAndPing(t, spec, secrets.NewMemory()); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteSSE(t *testing.T) {
	url := testServer(t, true, "X-Key", "k1")
	spec := pluginapi.ServerSpec{URL: url, Transport: pluginapi.TransportSSE, Auth: pluginapi.AuthHeaders, Headers: map[string]string{"X-Key": "{t-key}"}}
	store := secrets.NewMemory()
	_ = store.Set("t-key", "k1")
	if err := connectAndPing(t, spec, store); err != nil {
		t.Fatal(err)
	}
}

func TestCommandEnvSecrets(t *testing.T) {
	store := secrets.NewMemory()
	c := &Connector{Secrets: store}
	spec := pluginapi.ServerSpec{Command: "srv", Env: map[string]string{"TOKEN": "{tok}", "MODE": "fast"}}
	if _, err := c.command("x", spec); !errors.Is(err, ErrNeedsReauth) {
		t.Fatalf("missing secret: %v", err)
	}
	_ = store.Set("tok", "s3cret")
	cmd, err := c.command("x", spec)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]bool{}
	for _, kv := range cmd.Env {
		env[kv] = true
	}
	if !env["TOKEN=s3cret"] || !env["MODE=fast"] || len(cmd.Env) < len(os.Environ()) {
		t.Fatalf("env missing entries")
	}
}
