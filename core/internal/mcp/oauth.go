package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"

	"github.com/BorysSerbyn/process-automator/core/internal/config"
	"github.com/BorysSerbyn/process-automator/core/internal/filelock"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/secrets"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// storedAuth is what's persisted per remote server: the dynamically registered
// client and the current token (refresh tokens rotate, so it's rewritten on refresh).
type storedAuth struct {
	ClientID     string           `json:"clientId"`
	ClientSecret string           `json:"clientSecret,omitempty"`
	TokenURL     string           `json:"tokenUrl"`
	AuthStyle    oauth2.AuthStyle `json:"authStyle"`
	Scopes       []string         `json:"scopes,omitempty"`
	Token        *oauth2.Token    `json:"token"`
}

func authKey(server string) string { return "oauth/" + server }

func (s *storedAuth) oauthConfig() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     s.ClientID,
		ClientSecret: s.ClientSecret,
		Endpoint:     oauth2.Endpoint{TokenURL: s.TokenURL, AuthStyle: s.AuthStyle},
		Scopes:       s.Scopes,
	}
}

func loadAuth(store secrets.Store, server string) (*storedAuth, error) {
	raw, err := store.Get(authKey(server))
	if err != nil {
		return nil, err
	}
	var a storedAuth
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		return nil, fmt.Errorf("stored auth for %s: %w", server, err)
	}
	return &a, nil
}

func saveAuth(store secrets.Store, server string, a *storedAuth) error {
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	return store.Set(authKey(server), string(raw))
}

// persistingTokenSource refreshes a server's token and saves it (refresh
// tokens rotate). Refreshes are serialized across processes per credential:
// two runs refreshing the same token at once would leave one holding a
// revoked refresh token (a false needs-reauth). So a refresh takes the
// credential's lock, re-reads the stored token (another process may have
// just rotated it) and refreshes only if that one is stale too.
type persistingTokenSource struct {
	mu      sync.Mutex
	store   secrets.Store
	server  string
	auth    *storedAuth
	lockDir string // where credential locks live; "" for none
}

func (p *persistingTokenSource) Token() (*oauth2.Token, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.auth.Token.Valid() {
		return p.auth.Token, nil
	}
	if p.lockDir != "" {
		if err := os.MkdirAll(p.lockDir, 0o700); err != nil {
			return nil, err
		}
		unlock, err := filelock.Lock(filepath.Join(p.lockDir, "oauth-"+p.server+".lock"), true)
		if err != nil {
			return nil, err
		}
		defer unlock()
	}
	// The stored token, which another run may have just refreshed. If it
	// can't be read, refreshing with ours could present a used refresh token.
	stored, err := loadAuth(p.store, p.server)
	switch {
	case errors.Is(err, secrets.ErrNotFound):
	case err != nil:
		return nil, err
	case stored.Token != nil:
		p.auth.Token = stored.Token
		if stored.Token.Valid() {
			return stored.Token, nil // refreshed by another run meanwhile
		}
	}
	t, err := p.auth.oauthConfig().TokenSource(context.Background(), p.auth.Token).Token()
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) {
			// Refresh rejected (expired/revoked): the user has to sign in again.
			return nil, &NeedsReauthError{Server: p.server, Cause: err}
		}
		return nil, err
	}
	if p.auth.Token == nil || t.AccessToken != p.auth.Token.AccessToken || t.RefreshToken != p.auth.Token.RefreshToken {
		p.auth.Token = t
		if err := saveAuth(p.store, p.server, p.auth); err != nil {
			return nil, fmt.Errorf("persist token for %s: %w", p.server, err)
		}
	}
	return t, nil
}

// unattendedAuth builds an OAuth handler from stored credentials. It never opens a
// browser: if the server demands a new sign-in, connecting fails with NeedsReauthError.
func (c *Connector) unattendedAuth(name string, srv pluginapi.ServerSpec) (auth.OAuthHandler, error) {
	stored, err := loadAuth(c.Secrets, name)
	if errors.Is(err, secrets.ErrNotFound) {
		return nil, &NeedsReauthError{Server: name, Cause: errors.New("not signed in")}
	}
	if err != nil {
		return nil, err
	}
	ts := &persistingTokenSource{store: c.Secrets, server: name, auth: stored, lockDir: c.LockDir}
	return auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{
		PreregisteredClient: &oauthex.ClientCredentials{ClientID: stored.ClientID},
		RedirectURL:         "http://127.0.0.1/callback", // never used: the fetcher below refuses
		ScopeFilter:         fixedScopes(srv.Scopes),
		InitialTokenSource:  ts,
		AuthorizationCodeFetcher: func(context.Context, *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			return nil, &NeedsReauthError{Server: name, Cause: errors.New("server requested a new sign-in")}
		},
	})
}

// loginTimeout is how long a sign-in waits for the browser to come back, so
// an abandoned one frees its callback port and whoever waits on it.
const loginTimeout = 10 * time.Minute

// Login signs in to a remote server interactively: registers a client, opens the
// browser via openURL, waits for the loopback redirect, and stores the credentials.
func (c *Connector) Login(ctx context.Context, name string, openURL func(string) error) error {
	srv, ok := c.Servers[name]
	if !ok {
		return fmt.Errorf("unknown mcp server %q", name)
	}
	if !config.OAuth(srv) {
		return fmt.Errorf("%s doesn't use OAuth sign-in", name)
	}
	port := 0
	if srv.OAuthClient != nil {
		port = srv.OAuthClient.RedirectPort
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("listen for sign-in redirect: %w", err)
	}
	defer ln.Close()
	redirect := fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port)

	cfg := &auth.AuthorizationCodeHandlerConfig{
		ScopeFilter:              fixedScopes(srv.Scopes),
		RequestRefreshToken:      true,
		AuthorizationCodeFetcher: loopbackFetcher(ln, openURL),
	}
	if oc := srv.OAuthClient; oc != nil {
		// Pre-registered app (no dynamic registration). Its registration lists the
		// localhost redirect, which some providers (Slack) treat as a desktop redirect.
		redirect = fmt.Sprintf("http://localhost:%d/callback", oc.RedirectPort)
		creds, err := c.preregistered(oc)
		if err != nil {
			return err
		}
		cfg.PreregisteredClient = creds
	} else {
		cfg.DynamicClientRegistrationConfig = &auth.DynamicClientRegistrationConfig{
			Metadata: &oauthex.ClientRegistrationMetadata{
				ClientName:              c.Name,
				RedirectURIs:            []string{redirect},
				GrantTypes:              []string{"authorization_code", "refresh_token"},
				ResponseTypes:           []string{"code"},
				TokenEndpointAuthMethod: "none",
			},
		}
	}
	cfg.RedirectURL = redirect

	var saved bool
	cfg.NewTokenSource = func(_ context.Context, oc *oauth2.Config, tok *oauth2.Token) (oauth2.TokenSource, error) {
		stored := &storedAuth{
			ClientID: oc.ClientID, ClientSecret: oc.ClientSecret,
			TokenURL: oc.Endpoint.TokenURL, AuthStyle: oc.Endpoint.AuthStyle,
			Scopes: oc.Scopes, Token: tok,
		}
		if err := saveAuth(c.Secrets, name, stored); err != nil {
			return nil, err
		}
		saved = true
		return &persistingTokenSource{store: c.Secrets, server: name, auth: stored, lockDir: c.LockDir}, nil
	}
	handler, err := auth.NewAuthorizationCodeHandler(cfg)
	if err != nil {
		return err
	}
	transport, err := c.remoteTransport(name, srv, handler)
	if err != nil {
		return err
	}
	session, err := c.connect(ctx, name, transport)
	if err != nil {
		return err
	}
	defer session.Close()
	if !saved {
		return errors.New("signed in but no token was issued")
	}
	return nil
}

// preregistered loads a pre-registered client's credentials from the secrets store.
func (c *Connector) preregistered(oc *pluginapi.OAuthClient) (*oauthex.ClientCredentials, error) {
	id, err := c.Secrets.Get(oc.ClientIDKey)
	if err != nil {
		return nil, fmt.Errorf("client id (%s): %w", oc.ClientIDKey, err)
	}
	creds := &oauthex.ClientCredentials{ClientID: id}
	if oc.ClientSecretKey != "" {
		secret, err := c.Secrets.Get(oc.ClientSecretKey)
		if err != nil {
			return nil, fmt.Errorf("client secret (%s): %w", oc.ClientSecretKey, err)
		}
		creds.ClientSecretAuth = &oauthex.ClientSecretAuth{ClientSecret: secret}
	}
	return creds, nil
}

// Logout forgets a remote server's stored credentials.
func (c *Connector) Logout(name string) error { return c.Secrets.Delete(authKey(name)) }

// SignedIn reports whether credentials are stored for a remote server.
func (c *Connector) SignedIn(name string) (bool, error) {
	_, err := loadAuth(c.Secrets, name)
	if errors.Is(err, secrets.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func fixedScopes(scopes []string) func([]string) []string {
	if len(scopes) == 0 {
		return nil
	}
	return func([]string) []string { return scopes }
}

// loopbackFetcher opens the authorization URL and waits for the redirect on ln.
func loopbackFetcher(ln net.Listener, openURL func(string) error) auth.AuthorizationCodeFetcher {
	return func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
		results := make(chan *auth.AuthorizationResult, 1)
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/callback" {
				http.NotFound(w, r)
				return
			}
			q := r.URL.Query()
			if e := q.Get("error"); e != "" {
				_, _ = io.WriteString(w, "Sign-in failed: "+e+". You can close this tab.")
			} else {
				_, _ = io.WriteString(w, "Signed in to "+product.Name+". You can close this tab.")
			}
			select {
			case results <- &auth.AuthorizationResult{Code: q.Get("code"), State: q.Get("state"), Iss: q.Get("iss")}:
			default:
			}
		})}
		go func() { _ = srv.Serve(ln) }()
		defer srv.Close()

		if err := openURL(args.URL); err != nil {
			return nil, fmt.Errorf("open browser: %w", err)
		}
		select {
		case res := <-results:
			if res.Code == "" {
				return nil, errors.New("sign-in was not completed")
			}
			return res, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(loginTimeout):
			return nil, fmt.Errorf("sign-in wasn't finished within %s: start it again", loginTimeout)
		}
	}
}
