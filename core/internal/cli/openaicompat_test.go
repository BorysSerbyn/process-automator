package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/engine/enginetest"
	"github.com/BorysSerbyn/process-automator/core/internal/secrets"
)

// On api, setup offers the OpenAI-compatible server as experimental and
// untested; routines that only use it need no Anthropic key; its key signs in
// like Anthropic's; doctor says when it isn't answering.
func TestOpenAICompatSetup(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	enginetest.Set(t, cfg, "llm.backend", "api")
	a := enginetest.Load(t, cfg)
	_ = testSecrets.Delete(engine.AnthropicKey)
	t.Cleanup(func() { _ = testSecrets.Delete(engine.OpenAICompatKey) })

	backend := func() SetupStep {
		t.Helper()
		out, err := cli(t, cfg, "setup", "--json")
		var got SetupOutput
		if err != nil || json.Unmarshal([]byte(out), &got) != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		for _, s := range got.Steps {
			if s.Step == stepBackend {
				return s
			}
		}
		t.Fatal("no backend step")
		return SetupStep{}
	}
	s := backend()
	if s.Done || s.SignIn != "anthropic" || !strings.Contains(s.Ask, "Can also run local models such as Ollama (experimental, untested)") {
		t.Errorf("no routines: %+v", s)
	}
	var offered int
	for _, c := range s.Commands {
		if strings.Contains(c, "openai-compat") && strings.Contains(c, "experimental and untested") {
			offered++
		}
	}
	if offered != 2 {
		t.Errorf("commands: %q", s.Commands)
	}

	l, _ := net.Listen("tcp", "127.0.0.1:0")
	down := "http://" + l.Addr().String() + "/v1"
	_ = l.Close()
	enginetest.Set(t, cfg, engine.OpenAICompatURLSetting, down)
	_ = os.MkdirAll(a.Layout.Routines(), 0o700)
	_ = os.WriteFile(filepath.Join(a.Layout.Routines(), "local.prompt"), []byte("---\nmodel: openai-compat/qwen3:8b\n---\nGo. {{> memories}}\n"), 0o600)
	if s := backend(); !s.Done || s.Status != "Runs with api, ready" {
		t.Errorf("only an OpenAI-compatible model: %+v", s)
	}
	out, err := cli(t, cfg, "doctor")
	if err == nil || !strings.Contains(out, "its model openai-compat/qwen3:8b needs the OpenAI-compatible server: nothing answers at "+down) {
		t.Errorf("doctor: %v\n%s", err, out)
	}

	root := NewRoot(nil)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetIn(strings.NewReader("sk-hosted\n"))
	root.SetArgs([]string{"--config", cfg, "auth", "login", "openai-compat"})
	if err := root.Execute(); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	if got, _ := testSecrets.Get(engine.OpenAICompatKey); got != "sk-hosted" {
		t.Errorf("stored key %q", got)
	}
	if out, _ := cli(t, cfg, "auth", "status"); !strings.Contains(out, "openai-compat") {
		t.Errorf("auth status:\n%s", out)
	}
	if _, err := cli(t, cfg, "auth", "logout", "openai-compat"); err != nil {
		t.Fatal(err)
	}
	if _, err := testSecrets.Get(engine.OpenAICompatKey); err == nil {
		t.Error("logout kept the key")
	}
}

// auth status tells a secret that can't be read (a locked Keychain or
// keyring) from one that isn't stored.
func TestAuthStatusUnreadable(t *testing.T) {
	a, err := engine.Load(filepath.Join(t.TempDir(), "config.yaml"), nil, lockedSecrets{secrets.NewMemory()})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := authStatuses(a)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]AuthStatus{}
	for _, r := range rows {
		got[r.Server] = r
	}
	for _, name := range []string{"anthropic", "claude-code", "openai-compat"} {
		if r, ok := got[name]; !ok || r.SignedIn || !strings.Contains(r.Problem, "can't be read: the keychain is locked") {
			t.Errorf("%s: %+v (listed %v)", name, r, ok)
		}
	}
}

// lockedSecrets can't be read, like a locked Keychain or keyring.
type lockedSecrets struct{ *secrets.Memory }

func (lockedSecrets) Get(string) (string, error) { return "", errors.New("the keychain is locked") }
