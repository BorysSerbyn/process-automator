package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A release's notes come from the GitHub API: a public repository's with no
// token, a private one's with its token. They're kept a day; when GitHub
// can't be read, the copy kept stands in.
func TestNotes(t *testing.T) {
	var asked atomic.Int32
	var down atomic.Bool
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		auth := r.Header.Get("Authorization")
		switch {
		case down.Load():
			w.WriteHeader(http.StatusBadGateway)
		case r.URL.Path == "/repos/o/public/releases/tags/v1.2.0" && auth == "":
			_, _ = w.Write([]byte(`{"tag_name":"v1.2.0","body":"## What's new\n- faster\n","html_url":"https://example.com/v1.2.0","published_at":"2026-11-23T10:00:00Z"}`))
		case r.URL.Path == "/repos/o/private/releases/tags/v1.2.0" && auth == "Bearer pt-token":
			_, _ = w.Write([]byte(`{"tag_name":"v1.2.0","body":"private notes"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	ctx := context.Background()

	public := Source{Web: "http://127.0.0.1:1", API: api.URL, Repo: "o/public", Token: "leaked"}
	n, err := public.Notes(ctx, "v1.2.0")
	if err != nil || n.Body != "## What's new\n- faster" || n.URL != "https://example.com/v1.2.0" || n.PublishedAt == nil {
		t.Fatalf("public: %+v, %v", n, err)
	}
	if _, err := public.Notes(ctx, "v9.0.0"); err == nil || !strings.Contains(err.Error(), "no release v9.0.0") {
		t.Fatalf("missing: %v", err)
	}
	private := Source{API: api.URL, Repo: "o/private", Token: "pt-token"}
	if n, err := private.Notes(ctx, "v1.2.0"); err != nil || n.Body != "private notes" {
		t.Fatalf("private: %+v, %v", n, err)
	}
	if _, err := private.Notes(ctx, "main"); err == nil {
		t.Fatal("a branch's notes")
	}

	dir, now := t.TempDir(), time.Now()
	asked.Store(0)
	for i := 0; i < 2; i++ {
		if n, err := CachedNotes(ctx, public, dir, "v1.2.0", now); err != nil || !strings.HasPrefix(n.Body, "## What's new") {
			t.Fatalf("cached: %+v, %v", n, err)
		}
	}
	if asked.Load() != 1 {
		t.Fatalf("asked GitHub %d times for notes kept", asked.Load())
	}
	down.Store(true)
	if n, err := CachedNotes(ctx, public, dir, "v1.2.0", now.Add(2*NotesFresh)); err != nil || !strings.HasPrefix(n.Body, "## What's new") || asked.Load() != 2 {
		t.Fatalf("stale, GitHub down: %+v, %v (asked %d)", n, err, asked.Load())
	}
	if _, err := CachedNotes(ctx, public, dir, "v1.3.0", now); err == nil {
		t.Fatal("none kept, GitHub down: no error")
	}
}
