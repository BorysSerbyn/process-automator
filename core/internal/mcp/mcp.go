// Package mcp connects to the MCP servers registered in config: local stdio
// commands (secrets passed via environment) and remote streamable-HTTP servers
// (OAuth, tokens persisted in the secrets store).
package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/secrets"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// ErrNeedsReauth means a server's credentials are missing, expired or revoked and
// the user must sign in again (`procauto auth login <server>`). Unattended runs
// report this as the "needs-reauth" outcome rather than a generic failure.
var ErrNeedsReauth = errors.New("needs re-authentication")

// NeedsReauthError names the server that needs re-authentication.
type NeedsReauthError struct {
	Server string
	Cause  error
}

func (e *NeedsReauthError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: needs re-authentication (run `"+product.Command+" auth login %s`): %v", e.Server, e.Server, e.Cause)
	}
	return fmt.Sprintf("%s: needs re-authentication (run `"+product.Command+" auth login %s`)", e.Server, e.Server)
}

// Is makes errors.Is(err, ErrNeedsReauth) work.
func (e *NeedsReauthError) Is(target error) bool { return target == ErrNeedsReauth }

// Unwrap returns the cause.
func (e *NeedsReauthError) Unwrap() error { return e.Cause }

// Session is an open connection to one MCP server.
type Session = sdk.ClientSession

// Connector opens sessions to configured servers.
type Connector struct {
	Servers map[string]pluginapi.ServerSpec
	Secrets secrets.Store
	// Name and Version are reported to servers as the client's.
	Name, Version string
	// Builtins are in-process servers, by name, reached over in-memory
	// transports. Each session builds its server for the run it serves
	// (zero outside a run).
	Builtins map[string]func(run pluginapi.Run) (*sdk.Server, error)
	// LockDir holds the per-credential locks that serialize OAuth refreshes
	// across processes; "" disables them (tests).
	LockDir string
}

// Connect opens a session to the named server without user interaction,
// for no routine run. Remote servers must have been signed in with Login first.
func (c *Connector) Connect(ctx context.Context, name string) (*Session, error) {
	return c.ConnectFor(ctx, name, pluginapi.Run{})
}

// ConnectFor opens a session to the named server for a routine's run: an
// in-process server is built for that run (external servers can't be told).
func (c *Connector) ConnectFor(ctx context.Context, name string, run pluginapi.Run) (*Session, error) {
	if newServer, ok := c.Builtins[name]; ok {
		srv, err := newServer(run)
		if err != nil {
			return nil, fmt.Errorf("start %s: %w", name, err)
		}
		clientT, serverT := sdk.NewInMemoryTransports()
		if _, err := srv.Connect(ctx, serverT, nil); err != nil {
			return nil, fmt.Errorf("start %s: %w", name, err)
		}
		return c.connect(ctx, name, clientT)
	}
	srv, ok := c.Servers[name]
	if !ok {
		return nil, fmt.Errorf("unknown plugin %q", name)
	}
	var transport sdk.Transport
	if srv.Remote() {
		var handler auth.OAuthHandler
		if srv.AuthMode() == pluginapi.AuthOAuth {
			h, err := c.unattendedAuth(name, srv)
			if err != nil {
				return nil, err
			}
			handler = h
		}
		t, err := c.remoteTransport(name, srv, handler)
		if err != nil {
			return nil, err
		}
		transport = t
	} else {
		cmd, err := c.command(name, srv)
		if err != nil {
			return nil, err
		}
		transport = &sdk.CommandTransport{Command: cmd}
	}
	return c.connect(ctx, name, transport)
}

// Pair connects to a local server without its secrets and lists its tools, so a
// server that issues its own credential (ServerSpec.Pairing) asks the user to
// approve this client. The session is closed again.
func (c *Connector) Pair(ctx context.Context, name string) error {
	srv, ok := c.Servers[name]
	if !ok || srv.Remote() {
		return fmt.Errorf("%s is not a local server", name)
	}
	cmd := exec.Command(srv.Command, srv.Args...)
	cmd.Dir = srv.Dir
	cmd.Env = os.Environ()
	for k, v := range srv.Env {
		if !strings.Contains(v, "{") { // leave out the ones that need secrets
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	s, err := c.connect(ctx, name, &sdk.CommandTransport{Command: cmd})
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	_, err = s.ListTools(ctx, nil)
	return err
}

// remoteTransport builds the transport for a remote server: streamable HTTP or
// SSE, sending the spec's headers (secrets filled in) with every request.
func (c *Connector) remoteTransport(name string, srv pluginapi.ServerSpec, handler auth.OAuthHandler) (sdk.Transport, error) {
	headers := http.Header{}
	for k, v := range srv.Headers {
		val, err := c.expand(name, v)
		if err != nil {
			return nil, err
		}
		headers.Set(k, val)
	}
	client := &http.Client{Transport: headerTransport{headers: headers, next: http.DefaultTransport}}
	if srv.Transport == pluginapi.TransportSSE {
		return &sdk.SSEClientTransport{Endpoint: srv.URL, HTTPClient: client}, nil
	}
	return &sdk.StreamableClientTransport{Endpoint: srv.URL, HTTPClient: client, OAuthHandler: handler, DisableStandaloneSSE: true}, nil
}

// headerTransport adds fixed headers to every request.
type headerTransport struct {
	headers http.Header
	next    http.RoundTripper
}

func (t headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if len(t.headers) > 0 {
		req = req.Clone(req.Context())
		for k, vs := range t.headers {
			req.Header[k] = vs
		}
	}
	return t.next.RoundTrip(req)
}

// expand fills a spec value's {secret-key} references from the secrets store;
// a missing secret means the server needs signing in.
func (c *Connector) expand(name, v string) (string, error) {
	return pluginapi.ExpandSecrets(v, func(key string) (string, error) {
		val, err := c.Secrets.Get(key)
		if errors.Is(err, secrets.ErrNotFound) {
			return "", &NeedsReauthError{Server: name, Cause: fmt.Errorf("secret %q not set", key)}
		}
		return val, err
	})
}

func (c *Connector) connect(ctx context.Context, name string, transport sdk.Transport) (*Session, error) {
	client := sdk.NewClient(&sdk.Implementation{Name: c.Name, Version: c.Version}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		var nr *NeedsReauthError
		if errors.As(err, &nr) {
			return nil, nr // drop transport wrapping; the reauth message is what matters
		}
		return nil, fmt.Errorf("connect %s: %w", name, err)
	}
	return session, nil
}

// command builds the stdio command; its env values get their secrets filled in.
func (c *Connector) command(name string, srv pluginapi.ServerSpec) (*exec.Cmd, error) {
	cmd := exec.Command(srv.Command, srv.Args...)
	cmd.Dir = srv.Dir
	cmd.Env = os.Environ()
	for k, v := range srv.Env {
		val, err := c.expand(name, v)
		if err != nil {
			return nil, err
		}
		cmd.Env = append(cmd.Env, k+"="+val)
	}
	return cmd, nil
}
