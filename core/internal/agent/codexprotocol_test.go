package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The implementations are kept oldest first, each from a later version.
func TestCodexProtocolsOrdered(t *testing.T) {
	for i := 1; i < len(codexProtocols); i++ {
		if !codexProtocols[i-1].Since().Less(codexProtocols[i].Since()) {
			t.Fatalf("implementation %d (%s) isn't newer than the one before (%s)",
				i, codexProtocols[i].Since(), codexProtocols[i-1].Since())
		}
	}
}

type fakeCodexProtocol struct {
	codex0_171_0
	since string
}

func (f fakeCodexProtocol) Since() Version { return mustVersion(f.since) }

// A Codex gets the newest implementation at or below its version, and one
// older than every implementation is refused, saying how to update it.
func TestCodexFor(t *testing.T) {
	saved := codexProtocols
	t.Cleanup(func() { codexProtocols = saved })
	older, newer := fakeCodexProtocol{since: "0.100.0"}, fakeCodexProtocol{since: "0.200.0"}
	codexProtocols = []CodexProtocol{older, newer}
	for v, want := range map[string]CodexProtocol{"0.100.0": older, "0.199.9": older, "0.200.0": newer, "1.0": newer} {
		if got, err := CodexFor(mustVersion(v)); err != nil || got != want {
			t.Errorf("%s: got %v, %v", v, got, err)
		}
	}
	_, err := CodexFor(mustVersion("0.99.9"))
	var old *TooOldError
	if !errors.As(err, &old) || old.Need.String() != "0.100.0" || old.Have.String() != "0.99.9" ||
		!strings.Contains(err.Error(), "Codex 0.99.9 is too old") || !strings.Contains(err.Error(), "npm install -g @openai/codex") {
		t.Fatalf("an older Codex: %v", err)
	}
	if NewestCodex() != newer || OldestCodex() != older {
		t.Fatal("newest and oldest")
	}
}

// The Codex at a path is picked by its --version (never run in tests).
func TestCodexAt(t *testing.T) {
	version := "codex-cli 0.171.0"
	t.Cleanup(SetVersionForTests(func(string) (string, error) { return version, nil }))
	dir := t.TempDir()
	path, cache := filepath.Join(dir, "codex"), filepath.Join(dir, "cli-versions.json")
	_ = os.WriteFile(path, []byte("x"), 0o700)
	if p, err := CodexAt(context.Background(), path, cache); err != nil || p != NewestCodex() {
		t.Fatalf("%v %v", p, err)
	}
	version = "codex-cli 0.1.0"
	_ = os.WriteFile(path, []byte("older"), 0o700)
	var old *TooOldError
	if _, err := CodexAt(context.Background(), path, cache); !errors.As(err, &old) {
		t.Fatalf("too old: %v", err)
	}
}

// `codex login status`: signed in exits 0; otherwise its output says why.
func TestCodexLoginProblem(t *testing.T) {
	p := NewestCodex()
	if args := strings.Join(p.LoginStatusArgs(), " "); !strings.HasSuffix(args, "login status") {
		t.Errorf("args %q", args)
	}
	for _, c := range []struct {
		out  string
		ok   bool
		want string
	}{{"Logged in using ChatGPT\n", true, ""}, {"Not logged in\n", false, "Not logged in"}, {"", false, "not signed in"}} {
		if got := p.LoginProblem(c.out, c.ok); got != c.want {
			t.Errorf("%q %v: %q, want %q", c.out, c.ok, got, c.want)
		}
	}
}
