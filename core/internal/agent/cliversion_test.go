package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVersions(t *testing.T) {
	v, err := ParseVersion("2.1.331 (Claude Code)\n")
	if err != nil || v.String() != "2.1.331" {
		t.Fatalf("parsed %v, %v", v, err)
	}
	if v, err := ParseVersion("codex-cli 0.171.0\n"); err != nil || v.String() != "0.171.0" {
		t.Fatalf("parsed %v, %v", v, err)
	}
	if _, err := ParseVersion("Claude Code"); err == nil {
		t.Fatal("no version parsed as one")
	}
	for _, c := range []struct {
		a, b string
		less bool
	}{{"2.1.9", "2.1.10", true}, {"2.1.10", "2.1.9", false}, {"2.1", "2.1.0", false}, {"2.1", "2.1.1", true}, {"3.0.0", "2.9.9", false}} {
		if got := mustVersion(c.a).Less(mustVersion(c.b)); got != c.less {
			t.Errorf("%s < %s: %v", c.a, c.b, got)
		}
	}
}

// `<cli> --version` runs once per binary, shared through the cache file by
// other processes, and again when the binary changes or an hour has passed.
// A failure is kept for a minute.
func TestInstalledVersionCached(t *testing.T) {
	calls, version, fail := 0, "2.1.331", error(nil)
	t.Cleanup(SetVersionForTests(func(string) (string, error) { calls++; return version, fail }))
	dir := t.TempDir()
	path, cache := filepath.Join(dir, "claude"), filepath.Join(dir, "cli-versions.json")
	if err := os.WriteFile(path, []byte("one"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for range 2 {
		if v, err := InstalledVersion(ctx, path, cache); err != nil || v.String() != "2.1.331" {
			t.Fatalf("%v, %v", v, err)
		}
	}
	forget := func() {
		cliVersions.Lock()
		cliVersions.m = map[string]cachedVersion{}
		cliVersions.Unlock()
	}
	forget() // another process: only the file
	if v, _ := InstalledVersion(ctx, path, cache); v.String() != "2.1.331" || calls != 1 {
		t.Fatalf("ran claude --version %d times", calls)
	}
	version = "2.2.0"
	if err := os.WriteFile(path, []byte("updated"), 0o700); err != nil {
		t.Fatal(err)
	}
	if v, _ := InstalledVersion(ctx, path, cache); v.String() != "2.2.0" || calls != 2 {
		t.Fatalf("after an update: %v (%d runs)", v, calls)
	}
	// An hour later (a shim whose file never changes), it's asked again.
	age := func(d time.Duration) {
		cliVersions.Lock()
		c := cliVersions.m[path]
		c.At = c.At.Add(-d)
		cliVersions.m[path] = c
		cliVersions.Unlock()
	}
	version = "2.3.0"
	age(versionTTL)
	if v, _ := InstalledVersion(ctx, path, cache); v.String() != "2.3.0" || calls != 3 {
		t.Fatalf("after an hour: %v (%d runs)", v, calls)
	}
	// A failure is kept for a minute, then asked again.
	fail = errors.New("hung")
	age(versionTTL)
	for range 2 {
		if _, err := InstalledVersion(ctx, path, cache); err == nil {
			t.Fatal("a failing --version gave a version")
		}
	}
	if calls != 4 {
		t.Fatalf("a failure wasn't kept: %d runs", calls)
	}
	fail = nil
	age(versionErrorTTL)
	if v, err := InstalledVersion(ctx, path, cache); err != nil || v.String() != "2.3.0" || calls != 5 {
		t.Fatalf("after a minute: %v %v (%d runs)", v, err, calls)
	}
}

// Two CLIs share the cache file, each under its own binary; a version
// that can't be read is said with the binary's name.
func TestInstalledVersionByBinary(t *testing.T) {
	t.Cleanup(SetVersionForTests(func(name string) (string, error) {
		switch name {
		case "claude":
			return "2.1.331 (Claude Code)", nil
		case "codex":
			return "codex-cli 0.171.0", nil
		}
		return "", errors.New("unknown")
	}))
	dir := t.TempDir()
	cache := filepath.Join(dir, "cli-versions.json")
	for name, want := range map[string]string{"claude": "2.1.331", "codex": "0.171.0"} {
		path := filepath.Join(dir, name)
		_ = os.WriteFile(path, []byte(name), 0o700)
		if v, err := InstalledVersion(context.Background(), path, cache); err != nil || v.String() != want {
			t.Errorf("%s: %v %v", name, v, err)
		}
	}
	if m := readVersionCache(cache); len(m) != 2 {
		t.Errorf("cache file: %v", m)
	}
	other := filepath.Join(dir, "other")
	_ = os.WriteFile(other, nil, 0o700)
	if _, err := InstalledVersion(context.Background(), other, cache); err == nil || !strings.HasPrefix(err.Error(), "other --version: ") {
		t.Errorf("a failing --version: %v", err)
	}
	if _, err := InstalledVersion(context.Background(), filepath.Join(dir, "gone"), cache); err == nil {
		t.Error("a missing binary has a version")
	}
}
