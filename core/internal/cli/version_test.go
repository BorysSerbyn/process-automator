package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	origAPI, origRepo := product.ReleaseAPI, product.ReleaseRepo
	product.ReleaseAPI, product.ReleaseRepo = releases.URL, "pt/none"
	t.Cleanup(func() { product.ReleaseAPI, product.ReleaseRepo = origAPI, origRepo })
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
	origAPI, origRepo := product.ReleaseAPI, product.ReleaseRepo
	product.ReleaseAPI, product.ReleaseRepo = releases.URL, "pt/private"
	t.Cleanup(func() { product.ReleaseAPI, product.ReleaseRepo = origAPI, origRepo })
	t.Setenv("GH_TOKEN", "pt-token")

	cfg := filepath.Join(t.TempDir(), "config.yaml")
	// A checkout that isn't one: reaching it would fail differently.
	if _, err := cli(t, cfg, "settings", "set", "update.source-dir", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	out, err := cli(t, cfg, "update", "--check", "--json")
	if err == nil || !strings.Contains(out, "can't read the releases") || !strings.Contains(out, "404") || strings.Contains(out, "fetch tags") {
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
