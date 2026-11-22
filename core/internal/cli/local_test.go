package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/engine/enginetest"
	"github.com/BorysSerbyn/process-automator/core/internal/secrets"
	"github.com/BorysSerbyn/process-automator/core/internal/usage"
)

// backendStepOf is setup --json's backend step.
func backendStepOf(t *testing.T, cfg string) engine.SetupStep {
	t.Helper()
	out, err := cli(t, cfg, "setup", "--json")
	var got engine.SetupOutput
	if err != nil || json.Unmarshal([]byte(out), &got) != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, s := range got.Steps {
		if s.Step == engine.StepBackend {
			return s
		}
	}
	t.Fatal("no backend step")
	return engine.SetupStep{}
}

// On local, setup --json's backend step isn't done while the server's URL or
// the model is missing: its status names what's missing, and its ask and
// commands say how to set them.
func TestLocalSetupJSON(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	enginetest.Set(t, cfg, "llm.backend", "local")
	s := backendStepOf(t, cfg)
	if s.Done || s.SignIn != "" || !strings.Contains(s.Status, "Runs with local (experimental, untested), but no model is set") ||
		!strings.Contains(s.Ask, "- local: Your own model server, e.g. Ollama (experimental, untested)") ||
		!strings.Contains(s.Ask, "On local: which model should routines run on") ||
		!strings.Contains(s.Ask, "is the server at http://localhost:11434/v1 (Ollama on this machine)") {
		t.Errorf("no model: %+v", s)
	}
	hasCommand := func(s engine.SetupStep, prefix string) bool {
		return slices.ContainsFunc(s.Commands, func(c string) bool { return strings.HasPrefix(c, prefix) })
	}
	for _, want := range []string{
		"procauto settings set llm.backend <claude-code|claude-api|local|codex>",
		"procauto settings set llm.local.url <url>   # local only",
		"procauto settings set llm.local.model <model>   # local only",
		"procauto auth login local --browser   # local only: when the server needs an API key",
	} {
		if !hasCommand(s, want) {
			t.Errorf("no %q in %q", want, s.Commands)
		}
	}

	enginetest.Set(t, cfg, engine.LocalURLSetting, "")
	if s := backendStepOf(t, cfg); s.Done || !strings.Contains(s.Status, "no server URL is set") || !strings.Contains(s.Status, "no model is set") {
		t.Errorf("no URL, no model: %+v", s)
	}
	enginetest.Set(t, cfg, engine.LocalURLSetting, "http://gpu-box:11434/v1")
	enginetest.Set(t, cfg, engine.LocalModelSetting, "qwen3:8b")
	s = backendStepOf(t, cfg)
	if !s.Done || s.Status != "Runs with local (experimental, untested), ready" || strings.Contains(s.Ask, "On local") ||
		!hasCommand(s, "procauto settings set llm.local.url <url>   # local only, experimental and untested: when the server isn't at http://gpu-box:11434/v1") {
		t.Errorf("ready: %+v", s)
	}

	out, err := cli(t, cfg, "usage", "--json")
	var r usage.Report
	if err != nil || json.Unmarshal([]byte(out), &r) != nil || r.Kind != usage.KindNone || r.Over {
		t.Errorf("usage on local: %v\n%s", err, out)
	}
	// claude-api's budgets don't apply; the local settings do.
	out, _ = cli(t, cfg, "settings", "list")
	if !strings.Contains(out, "[only used with llm.backend claude-api]") || strings.Contains(out, "[only used with llm.backend local") {
		t.Errorf("settings list on local:\n%s", out)
	}
}

// On claude-api, routines that run only local/<model> models need no
// Anthropic key; the local server's key signs in as local (or openai-compat,
// its former name); doctor says when the server isn't answering.
func TestClaudeAPILocalRoutines(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	enginetest.Set(t, cfg, "llm.backend", "claude-api")
	a := enginetest.Load(t, cfg)
	_ = testSecrets.Delete(engine.AnthropicKey)
	t.Cleanup(func() {
		for _, k := range engine.LocalKeys {
			_ = testSecrets.Delete(k)
		}
	})
	if s := backendStepOf(t, cfg); s.Done || s.SignIn != "anthropic" {
		t.Errorf("no routines: %+v", s)
	}
	// llm.local.url is claude-api's too (local/<model> routines); llm.local.model isn't.
	if out, _ := cli(t, cfg, "settings", "list"); !strings.Contains(out, "[only used with llm.backend local] The model the local backend runs") ||
		strings.Contains(out, "[only used with llm.backend local or claude-api]") {
		t.Errorf("settings list on claude-api:\n%s", out)
	}

	l, _ := net.Listen("tcp", "127.0.0.1:0")
	down := "http://" + l.Addr().String() + "/v1"
	_ = l.Close()
	enginetest.Set(t, cfg, engine.LocalURLSetting, down)
	_ = os.MkdirAll(a.Layout.Routines(), 0o700)
	_ = os.WriteFile(filepath.Join(a.Layout.Routines(), "local.prompt"), []byte("---\nmodel: local/qwen3:8b\n---\nGo. {{> memories}}\n"), 0o600)
	if s := backendStepOf(t, cfg); !s.Done || s.Status != "Runs with claude-api, ready" {
		t.Errorf("only a local model: %+v", s)
	}
	out, err := cli(t, cfg, "doctor")
	if err == nil || !strings.Contains(out, "its model local/qwen3:8b needs the local server: nothing answers at "+down) {
		t.Errorf("doctor: %v\n%s", err, out)
	}

	login := func(name, key string) {
		t.Helper()
		root := NewRoot(nil)
		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetIn(strings.NewReader(key + "\n"))
		root.SetArgs([]string{"--config", cfg, "auth", "login", name})
		if err := root.Execute(); err != nil {
			t.Fatalf("%v\n%s", err, buf.String())
		}
	}
	login("local", "sk-hosted")
	if got, _ := testSecrets.Get(engine.LocalKey); got != "sk-hosted" {
		t.Errorf("stored key %q", got)
	}
	login("openai-compat", "sk-again")
	if got, _ := testSecrets.Get(engine.LocalKey); got != "sk-again" {
		t.Errorf("stored key through the former name %q", got)
	}
	if out, _ := cli(t, cfg, "auth", "status"); !strings.Contains(out, "local") {
		t.Errorf("auth status:\n%s", out)
	}
	_ = testSecrets.Set(engine.LocalKeys[1], "sk-former")
	if _, err := cli(t, cfg, "auth", "logout", "local"); err != nil {
		t.Fatal(err)
	}
	for _, k := range engine.LocalKeys {
		if _, err := testSecrets.Get(k); err == nil {
			t.Errorf("logout kept %s", k)
		}
	}
}

// auth status tells a secret that can't be read (a locked Keychain or
// keyring) from one that isn't stored.
func TestAuthStatusUnreadable(t *testing.T) {
	a, err := engine.Load(filepath.Join(t.TempDir(), "config.yaml"), nil, lockedSecrets{secrets.NewMemory()})
	if err != nil {
		t.Fatal(err)
	}
	rows := authStatuses(a)
	got := map[string]AuthStatus{}
	for _, r := range rows {
		got[r.Server] = r
	}
	for _, name := range []string{"anthropic", "claude-code", "local"} {
		if r, ok := got[name]; !ok || r.SignedIn || !strings.Contains(r.Problem, "can't be read: the keychain is locked") {
			t.Errorf("%s: %+v (listed %v)", name, r, ok)
		}
	}
}

// lockedSecrets can't be read, like a locked Keychain or keyring.
type lockedSecrets struct{ *secrets.Memory }

func (lockedSecrets) Get(string) (string, error) { return "", errors.New("the keychain is locked") }
