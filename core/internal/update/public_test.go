package update

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// fakeReleasePages serves a public repository's releases the way GitHub's
// web pages do: /o/r/releases/latest redirects to the latest release's tag
// page (or, with none, to the releases page), and a release's files are
// downloaded from /o/r/releases/download/<tag>/<name>, which redirects to
// where the file is stored. Any request carrying a token fails the test.
type fakeReleasePages struct {
	*httptest.Server
	mu       sync.Mutex
	tag      string            // the latest release; "" for none
	files    map[string][]byte // its files
	status   map[string]int    // paths answered with this status instead
	requests []string
}

func newFakeReleasePages(t *testing.T, tag string, files map[string][]byte) *fakeReleasePages {
	t.Helper()
	f := &fakeReleasePages{tag: tag, files: files, status: map[string]int{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests = append(f.requests, r.URL.Path)
		if h := r.Header.Get("Authorization"); h != "" {
			t.Errorf("%s was sent an Authorization header", r.URL.Path)
		}
		for name, vals := range r.Header {
			for _, v := range vals {
				if strings.Contains(v, "leaked") {
					t.Errorf("%s was sent a token in %s", r.URL.Path, name)
				}
			}
		}
		if code, ok := f.status[r.URL.Path]; ok {
			w.WriteHeader(code)
			return
		}
		switch path := r.URL.Path; {
		case path == "/o/r/releases/latest":
			if f.tag == "" {
				http.Redirect(w, r, "/o/r/releases", http.StatusFound)
				return
			}
			http.Redirect(w, r, "/o/r/releases/tag/"+f.tag, http.StatusFound)
		case strings.HasPrefix(path, "/o/r/releases/download/"+f.tag+"/") && f.tag != "":
			name := strings.TrimPrefix(path, "/o/r/releases/download/"+f.tag+"/")
			if _, ok := f.files[name]; !ok {
				http.NotFound(w, r)
				return
			}
			http.Redirect(w, r, "/storage/"+name, http.StatusFound)
		case strings.HasPrefix(path, "/storage/"):
			_, _ = w.Write(f.files[strings.TrimPrefix(path, "/storage/")])
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

// locked changes what the server serves, between requests.
func (f *fakeReleasePages) locked(change func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change()
}

// noSignIn makes running the GitHub CLI, or reading a token, fail the test:
// gh (the variable Token runs it through) fails it, a gh first on PATH
// records being run, and GH_TOKEN and GITHUB_TOKEN are set to a token the
// fake servers refuse to see.
func noSignIn(t *testing.T) {
	t.Helper()
	orig := gh
	gh = func(_ context.Context, args ...string) ([]byte, error) {
		t.Errorf("gh %s was run", strings.Join(args, " "))
		return nil, errors.New("no gh in tests")
	}
	if runtime.GOOS == "windows" {
		t.Cleanup(func() { gh = orig })
		return
	}
	dir := t.TempDir()
	ran := filepath.Join(dir, "gh-ran")
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\necho \"$@\" >> '"+ran+"'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GH_TOKEN", "leaked-gh-token")
	t.Setenv("GITHUB_TOKEN", "leaked-github-token")
	t.Cleanup(func() {
		gh = orig
		if b, err := os.ReadFile(ran); err == nil {
			t.Errorf("gh was run: %s", b)
		}
	})
}

// signInWords are what public mode's messages never mention.
var signInWords = regexp.MustCompile(`(?i)\bgh\b|token|sign[- ]?in|signed in|auth`)

func checkNoSignIn(t *testing.T, err error) {
	t.Helper()
	if err != nil && signInWords.MatchString(err.Error()) {
		t.Errorf("error mentions signing in: %v", err)
	}
}

// Public releases: the latest from where /releases/latest redirects, its
// files from its SHA256SUMS, downloaded and verified as strictly as private
// ones, and never with a token, gh or a sign-in.
func TestPublicReleases(t *testing.T) {
	noSignIn(t)
	ctx := context.Background()
	good := []byte("the artifact")
	files := map[string][]byte{
		"a-1.2.0-macos.zip": good,
		"install.sh":        []byte("#!/bin/sh"),
		SumsName:            []byte(sha(good) + "  a-1.2.0-macos.zip\n" + sha([]byte("#!/bin/sh")) + " *install.sh\n"),
	}
	srv := newFakeReleasePages(t, "v1.2.0", files)
	// The token a private source would send is never sent to the web pages.
	s := Source{Web: srv.URL + "/", Repo: "o/r", Token: "leaked-field-token", API: "http://127.0.0.1:1"}

	rel, err := s.Latest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range rel.Assets {
		names = append(names, a.Name)
	}
	if rel.Tag != "v1.2.0" || strings.Join(names, ",") != SumsName+",a-1.2.0-macos.zip,install.sh" {
		t.Fatalf("Latest = %s %v", rel.Tag, names)
	}
	path, err := s.Fetch(ctx, rel, "a-1.2.0-macos.zip", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != string(good) {
		t.Fatalf("saved %q", b)
	}
	srv.locked(func() {
		for _, p := range srv.requests {
			if strings.HasPrefix(p, "/repos/") {
				t.Errorf("the API was asked: %s", p)
			}
		}
	})

	// No file for this machine: it isn't in SHA256SUMS.
	_, err = s.Fetch(ctx, rel, "a-1.2.0-linux-arm64.tar.gz", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "has no a-1.2.0-linux-arm64.tar.gz") {
		t.Fatalf("missing file: %v", err)
	}
	checkNoSignIn(t, err)

	// Tampered: the checksum stays as strict.
	srv.locked(func() { files["a-1.2.0-macos.zip"] = []byte("swapped") })
	_, err = s.Fetch(ctx, rel, "a-1.2.0-macos.zip", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("tampered: %v", err)
	}
	srv.locked(func() { files["a-1.2.0-macos.zip"] = good })

	// Listed in SHA256SUMS but not there to download.
	srv.locked(func() { delete(files, "install.sh") })
	_, err = s.Fetch(ctx, rel, "install.sh", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("listed but missing: %v", err)
	}
	checkNoSignIn(t, err)
}

func TestPublicReleasesErrors(t *testing.T) {
	noSignIn(t)
	ctx := context.Background()
	sums := map[string][]byte{SumsName: []byte(sha([]byte("x")) + "  a.zip\n"), "a.zip": []byte("x")}
	for _, tc := range []struct {
		name, tag string
		files     map[string][]byte
		status    map[string]int
		want      string // in the error; "" for none
	}{
		{"no releases", "", nil, nil, ""},
		{"not a public repository", "v1.0.0", sums, map[string]int{"/o/r/releases/latest": 404}, "no public repository o/r"},
		{"server error", "v1.0.0", sums, map[string]int{"/o/r/releases/latest": 500}, "500"},
		{"no SHA256SUMS", "v1.0.0", map[string][]byte{"a.zip": []byte("x")}, nil, "has no SHA256SUMS"},
		{"SHA256SUMS fails", "v1.0.0", sums, map[string]int{"/storage/" + SumsName: 502}, "502"},
		{"not a version", "nightly", sums, nil, `"nightly" isn't a release version`},
		{"a pre-release", "v2.0.0-rc1", sums, nil, "isn't a release version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newFakeReleasePages(t, tc.tag, tc.files)
			srv.locked(func() { srv.status = tc.status })
			rel, err := Source{Web: srv.URL, Repo: "o/r"}.Latest(ctx)
			if tc.want == "" {
				if err != nil || rel.Tag != "" {
					t.Fatalf("Latest = %+v, %v; want none", rel, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			checkNoSignIn(t, err)
		})
	}

	// GitHub can't be reached.
	srv := newFakeReleasePages(t, "v1.0.0", sums)
	srv.Close()
	_, err := Source{Web: srv.URL, Repo: "o/r"}.Latest(ctx)
	if err == nil || !strings.Contains(err.Error(), "can't reach GitHub") {
		t.Fatalf("unreachable: %v", err)
	}
	checkNoSignIn(t, err)
}
