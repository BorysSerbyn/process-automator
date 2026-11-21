package cli

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/engine/enginetest"
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
	if s.Done || s.SignIn != "anthropic" || !strings.Contains(s.Ask, "experimental and untested, models on an OpenAI-compatible server") {
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
