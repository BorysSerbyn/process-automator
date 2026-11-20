package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/BorysSerbyn/process-automator/core/internal/pluginapi"
	"github.com/BorysSerbyn/process-automator/core/internal/secrets"
)

var testServers = map[string]pluginapi.ServerSpec{
	"remote": {URL: "https://example.com/mcp"},
	"paired": {Command: "/bin/true", Env: map[string]string{"CRED": "{paired-client-credential}"}},
}

// tokenServer is an OAuth token endpoint that rotates refresh tokens and
// rejects a used one, as real providers do.
type tokenServer struct {
	mu      sync.Mutex
	current string // the refresh token it accepts
	n       int    // refreshes served
}

func (ts *tokenServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if r.PostFormValue("refresh_token") != ts.current {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
		return
	}
	ts.n++
	ts.current = fmt.Sprintf("r%d", ts.n+1)
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"access_token":"a%d","refresh_token":%q,"token_type":"bearer","expires_in":3600}`, ts.n+1, ts.current)
}

func expiredAuth(url, refresh string) *storedAuth {
	return &storedAuth{ClientID: "c", TokenURL: url, Token: &oauth2.Token{
		AccessToken: "a1", RefreshToken: refresh, Expiry: time.Now().Add(-time.Minute),
	}}
}

func TestPersistingTokenSourceSavesRotatedTokens(t *testing.T) {
	ts := &tokenServer{current: "r1"}
	srv := httptest.NewServer(ts)
	defer srv.Close()
	store := secrets.NewMemory()
	p := &persistingTokenSource{store: store, server: "remote", auth: expiredAuth(srv.URL, "r1")}
	tok, err := p.Token()
	if err != nil || tok.AccessToken != "a2" {
		t.Fatalf("refresh: %+v %v", tok, err)
	}
	got, err := loadAuth(store, "remote")
	if err != nil || got.Token.RefreshToken != "r2" || got.ClientID != "c" {
		t.Fatalf("rotated token not persisted: %+v %v", got, err)
	}
	if _, err := p.Token(); err != nil || ts.n != 1 {
		t.Fatalf("a valid token was refreshed again (%d refreshes): %v", ts.n, err)
	}
}

func TestPersistingTokenSourceRefreshRejectedNeedsReauth(t *testing.T) {
	srv := httptest.NewServer(&tokenServer{current: "other"})
	defer srv.Close()
	p := &persistingTokenSource{store: secrets.NewMemory(), server: "remote", auth: expiredAuth(srv.URL, "r1")}
	if _, err := p.Token(); !errors.Is(err, ErrNeedsReauth) {
		t.Fatalf("err = %v, want ErrNeedsReauth", err)
	}
}

// concurrentRefreshes has n "processes" (each its own token source and copy
// of the stored auth) refresh the same expired token at once, and returns
// how many refreshes the provider served and any errors.
func concurrentRefreshes(t *testing.T, n int, lockDir string) (int, []error) {
	t.Helper()
	ts := &tokenServer{current: "r1"}
	srv := httptest.NewServer(ts)
	defer srv.Close()
	store := secrets.NewMemory()
	_ = saveAuth(store, "remote", expiredAuth(srv.URL, "r1"))
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a, _ := loadAuth(store, "remote")
			p := &persistingTokenSource{store: store, server: "remote", auth: a, lockDir: lockDir}
			if _, err := p.Token(); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return ts.n, errs
}

// Runs in several processes holding the same expired token refresh it once:
// the others pick up the rotated token instead of presenting a used refresh
// token (which the provider rejects: a false needs-reauth).
func TestConcurrentRefreshesShareOneRotation(t *testing.T) {
	if n, errs := concurrentRefreshes(t, 6, t.TempDir()); n != 1 || len(errs) > 0 {
		t.Fatalf("%d refreshes, errors %v; want 1 refresh, no errors", n, errs)
	}
}

func TestConnectWithoutStoredAuthNeedsReauth(t *testing.T) {
	c := &Connector{Servers: testServers, Secrets: secrets.NewMemory()}
	_, err := c.Connect(context.Background(), "remote")
	var nr *NeedsReauthError
	if !errors.As(err, &nr) || nr.Server != "remote" {
		t.Fatalf("err = %v, want NeedsReauthError for remote", err)
	}
}

func TestConnectStdioMissingSecretNeedsReauth(t *testing.T) {
	c := &Connector{Servers: testServers, Secrets: secrets.NewMemory()}
	if _, err := c.Connect(context.Background(), "paired"); !errors.Is(err, ErrNeedsReauth) {
		t.Fatalf("err = %v, want ErrNeedsReauth", err)
	}
}
