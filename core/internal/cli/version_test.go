package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/filelock"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/update"
)

func TestVersionJSON(t *testing.T) {
	var out bytes.Buffer
	root := NewRoot(nil)
	root.SetOut(&out)
	root.SetArgs([]string{"version", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", out.String(), err)
	}
	if got["version"] != product.Version {
		t.Fatalf("version = %q, want %q", got["version"], product.Version)
	}
}

func TestDescribeUpdate(t *testing.T) {
	for _, tc := range []struct {
		r    UpdateOutput
		want string
	}{
		{UpdateOutput{Current: "0.0.1-dev+abc"}, "No releases yet"},
		{UpdateOutput{Current: "1.0.0", Latest: "v1.0.0"}, "Up to date"},
		{UpdateOutput{Current: "1.0.0", Latest: "v1.1.0", UpdateAvailable: true}, "v1.1.0 is available"},
		{UpdateOutput{Current: "1.0.0", Latest: "v1.1.0", UpdateAvailable: true, Updated: true}, "Updated 1.0.0 → 1.1.0"},
	} {
		if got := describeUpdate(tc.r); !strings.HasPrefix(got, tc.want) {
			t.Errorf("%+v: %q, want prefix %q", tc.r, got, tc.want)
		}
	}
}

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.Local)
	for in, want := range map[string]time.Time{
		"7d":         now.AddDate(0, 0, -7),
		"12h":        now.Add(-12 * time.Hour),
		"2026-11-17": time.Date(2026, 11, 17, 0, 0, 0, 0, time.Local),
	} {
		if got, err := parseSince(in, now); err != nil || !got.Equal(want) {
			t.Errorf("parseSince(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := parseSince("last week", now); err == nil {
		t.Error("garbage accepted")
	}
}

func TestShortDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{20 * time.Minute: "20m", 12 * time.Hour: "12h", 90 * time.Minute: "1h30m", 45 * time.Second: "45s"} {
		if got := settings.DurationText(d); got != want {
			t.Errorf("settings.DurationText(%v) = %q, want %q", d, got, want)
		}
	}
}

// With no published release for this machine, update falls back to the
// source checkout's tags.
func TestUpdateFallsBackToSource(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	releases := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("[]")) }))
	defer releases.Close()
	privateReleases(t, releases.URL, "pt/none")
	t.Setenv("GH_TOKEN", "pt-token")

	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	origin, clone := t.TempDir(), filepath.Join(t.TempDir(), "clone")
	git(origin, "init", "-q")
	git(origin, "commit", "-q", "--allow-empty", "-m", "x")
	git(origin, "tag", "v99.0.0")
	git(origin, "clone", "-q", origin, clone)

	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if _, err := cli(t, cfg, "settings", "set", "update.source-dir", clone); err != nil {
		t.Fatal(err)
	}
	out, err := cli(t, cfg, "update", "--check", "--json")
	var res UpdateOutput
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || res.Latest != "v99.0.0" || !res.UpdateAvailable {
		t.Errorf("update --check: %s (%v)", out, err)
	}
}

// When the releases can't be read (no access, no network), update says why
// and stops: the source checkout is only for when they were read and none
// has a download for this machine.
func TestUpdateStopsWhenReleasesCantBeRead(t *testing.T) {
	releases := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }))
	defer releases.Close()
	privateReleases(t, releases.URL, "pt/private")
	t.Setenv("GH_TOKEN", "pt-token")

	cfg := filepath.Join(t.TempDir(), "config.yaml")
	// A checkout that isn't one: reaching it would fail differently.
	if _, err := cli(t, cfg, "settings", "set", "update.source-dir", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	out, err := cli(t, cfg, "update", "--check", "--json")
	if err == nil || !strings.Contains(out, "can't read the releases") || !strings.Contains(out, "404") || strings.Contains(out, "fetch tags") ||
		!strings.Contains(out, "does your GitHub account have access to pt/private") {
		t.Fatalf("update --check: %s (%v)", out, err)
	}
	// Installing records the failure as the update's progress.
	if _, err := cli(t, cfg, "update"); err == nil {
		t.Fatal("update succeeded")
	}
	out, _ = cli(t, cfg, "update", "progress", "--json")
	var p UpdateProgress
	if json.Unmarshal([]byte(out), &p) != nil || p.State != "failed" || !strings.Contains(p.Error, "can't read the releases") {
		t.Fatalf("update progress: %s", out)
	}
}

// releasesAt points the build's releases at a test server, restored after
// the test.
func releasesAt(t *testing.T, public bool, url, repo string) {
	t.Helper()
	origPublic, origWeb, origAPI, origRepo := product.PublicReleases, product.ReleaseWeb, product.ReleaseAPI, product.ReleaseRepo
	t.Cleanup(func() {
		product.PublicReleases, product.ReleaseWeb, product.ReleaseAPI, product.ReleaseRepo = origPublic, origWeb, origAPI, origRepo
	})
	product.PublicReleases, product.ReleaseRepo = public, repo
	if public {
		product.ReleaseWeb, product.ReleaseAPI = url, "http://127.0.0.1:1"
	} else {
		product.ReleaseAPI, product.ReleaseWeb = url, "http://127.0.0.1:1"
	}
}

// privateReleases: a private repository's, read through the API (url) with
// a sign-in.
func privateReleases(t *testing.T, url, repo string) { t.Helper(); releasesAt(t, false, url, repo) }

// An app with public releases reads them from GitHub's web pages: no token
// is looked for or sent (GH_TOKEN neither), gh never runs, and no message
// mentions signing in.
func TestUpdatePublicReleases(t *testing.T) {
	name, err := update.AssetName("99.0.0")
	if err != nil {
		t.Skip(err)
	}
	var mu sync.Mutex
	var sums []byte
	tag, gone := "v99.0.0", false
	pages := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		for h, vals := range r.Header {
			if h == "Authorization" || strings.Contains(strings.Join(vals, " "), "pt-token") {
				t.Errorf("%s was sent %s", r.URL.Path, h)
			}
		}
		switch r.URL.Path {
		case "/pt/public/releases/latest":
			if gone {
				http.NotFound(w, r)
				return
			}
			http.Redirect(w, r, "/pt/public/releases/tag/"+tag, http.StatusFound)
		case "/pt/public/releases/download/" + tag + "/SHA256SUMS":
			_, _ = w.Write(sums)
		default:
			http.NotFound(w, r)
		}
	}))
	defer pages.Close()
	releasesAt(t, true, pages.URL, "pt/public")
	t.Setenv("GH_TOKEN", "pt-token")
	t.Setenv("GITHUB_TOKEN", "pt-token")
	orig := findReleaseToken
	findReleaseToken = func(context.Context, string, string) (string, []string) {
		t.Error("looked for a GitHub sign-in")
		return "", nil
	}
	t.Cleanup(func() { findReleaseToken = orig })
	ghRan := filepath.Join(t.TempDir(), "gh-ran")
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\necho \"$@\" >> '"+ghRan+"'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	signIn := regexp.MustCompile(`(?i)\bgh\b|token|sign[- ]?in|signed in`)
	cfg := filepath.Join(t.TempDir(), "config.yaml")

	setSums := func(b string) { mu.Lock(); sums = []byte(b); mu.Unlock() }
	setSums(strings.Repeat("0", 64) + "  " + name + "\n")
	out, err := cli(t, cfg, "update", "--check", "--json")
	var res UpdateOutput
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || res.Latest != "v99.0.0" || !res.UpdateAvailable || res.Note != installNote {
		t.Errorf("update --check: %s (%v)", out, err)
	}

	// No download for this machine: the source checkout, as for private releases.
	setSums(strings.Repeat("0", 64) + "  other.zip\n")
	if _, err := cli(t, cfg, "settings", "set", "update.source-dir", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	out, err = cli(t, cfg, "update", "--check")
	if err == nil || !strings.Contains(out, "fetch tags") || signIn.MatchString(out) {
		t.Errorf("no download for this machine: %s (%v)", out, err)
	}

	// Not there (or not public): says so, and nothing about signing in.
	mu.Lock()
	gone = true
	mu.Unlock()
	out, err = cli(t, cfg, "update", "--check")
	if err == nil || !strings.Contains(out, "can't read the releases") || !strings.Contains(out, "no public repository pt/public") || signIn.MatchString(out) {
		t.Errorf("not found: %s (%v)", out, err)
	}
	if b, err := os.ReadFile(ghRan); err == nil {
		t.Errorf("gh was run: %s", b)
	}
	if !strings.Contains(newUpdateCmd().Long, "no sign-in") || strings.Contains(newUpdateCmd().Long, "GH_TOKEN") {
		t.Errorf("update's help: %s", newUpdateCmd().Long)
	}
}

// update progress: none before any update; installing while one holds the
// lock, or just started one; failed when one stopped without finishing.
func TestUpdateProgress(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	a, err := loadTest(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	state := func() UpdateProgress {
		t.Helper()
		p, err := updateProgress(a, now)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	if p := state(); p.State != "none" {
		t.Fatalf("%+v", p)
	}
	r := update.NewReporter(a.Layout.UpdateProgress(), "Starting the update")
	r.Tag("v9.0.0")
	if p := state(); p.State != "installing" || p.Tag != "v9.0.0" || p.Step != "Starting the update" {
		t.Fatalf("just started: %+v", p)
	}
	now = now.Add(time.Hour)
	if p := state(); p.State != "failed" || !strings.Contains(p.Error, "stopped before it finished") {
		t.Fatalf("stopped: %+v", p)
	}
	unlock, err := filelock.Lock(a.Layout.UpdateLock(), false)
	if err != nil {
		t.Fatal(err)
	}
	if p := state(); p.State != "installing" {
		t.Fatalf("locked: %+v", p)
	}
	unlock()
	r.Finish("Updated 1.0.0 → 9.0.0", nil)
	if p := state(); p.State != "done" || p.Step != "Updated 1.0.0 → 9.0.0" || p.FinishedAt == nil {
		t.Fatalf("done: %+v", p)
	}
}

// update --ask says what's available and installs nothing unless the answer
// is yes; without a terminal it refuses, and up to date it says so.
func TestUpdateAsksFirst(t *testing.T) {
	name, err := update.AssetName("99.0.0")
	if err != nil {
		t.Skip(err)
	}
	pages := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pt/public/releases/latest":
			http.Redirect(w, r, "/pt/public/releases/tag/v99.0.0", http.StatusFound)
		case "/pt/public/releases/download/v99.0.0/SHA256SUMS":
			_, _ = w.Write([]byte(strings.Repeat("0", 64) + "  " + name + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer pages.Close()
	releasesAt(t, true, pages.URL, "pt/public")
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	run := func(terminal bool, answer string) (string, error) {
		orig := askable
		askable = func() bool { return terminal }
		defer func() { askable = orig }()
		root := NewRoot(nil)
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetIn(strings.NewReader(answer))
		root.SetArgs([]string{"--config", cfg, "update", "--ask"})
		err := root.Execute()
		return out.String(), err
	}

	if out, err := run(false, ""); err == nil || !strings.Contains(out, "needs a terminal") {
		t.Errorf("no terminal: %s (%v)", out, err)
	}
	out, err := run(true, "n\n")
	if err != nil || !strings.Contains(out, "v99.0.0 is available") || !strings.Contains(out, installNote) || !strings.Contains(out, "Not updated.") {
		t.Errorf("answered no: %s (%v)", out, err)
	}
	a, err := loadTest(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p, err := updateProgress(a, time.Now()); err != nil || p.State != "none" {
		t.Errorf("answering no started an update: %+v (%v)", p, err)
	}
	// Yes goes on to install (which, this not being an installed app, fails
	// finding it).
	if out, err := run(true, "y\n"); err == nil || !strings.Contains(out, "Update to v99.0.0 failed") && !strings.Contains(out, "templates") {
		t.Errorf("answered yes: %s (%v)", out, err)
	}
}
