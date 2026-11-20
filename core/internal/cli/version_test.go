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

	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/version"
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
	if got["version"] != version.Version {
		t.Fatalf("version = %q, want %q", got["version"], version.Version)
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
	origAPI, origRepo := version.ReleaseAPI, version.ReleaseRepo
	version.ReleaseAPI, version.ReleaseRepo = releases.URL, "pt/none"
	t.Cleanup(func() { version.ReleaseAPI, version.ReleaseRepo = origAPI, origRepo })
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
