package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/BorysSerbyn/process-automator/core/internal/engine/enginetest"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// cli runs the command line against a fresh root with config at cfg.
func cli(t *testing.T, cfg string, args ...string) (string, error) {
	t.Helper()
	root := NewRoot(nil)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"--config", cfg}, args...))
	err := root.Execute()
	return out.String(), err
}

// signInServer is a stand-in remote MCP server that wants sign-in (401).
func signInServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/mcp"
}

func TestPluginsAddRemove(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	remote := signInServer(t)
	if _, err := cli(t, cfg, "plugins", "add", "gone", "--url", "http://127.0.0.1:1/mcp"); err == nil {
		t.Fatal("an unreachable server was added")
	}
	_ = os.WriteFile(cfg, []byte("# keep me\nsettings:\n  llm.backend: claude-code\n"), 0o600)
	if _, err := cli(t, cfg, "plugins", "add", "acme", "--url", remote, "--auth", "headers"); err == nil {
		t.Fatal("headers auth without a secret reference accepted")
	}
	out, err := cli(t, cfg, "plugins", "add", "acme", "--url", remote, "--auth", "headers",
		"--header", "Authorization=Bearer {acme-key}")
	if err != nil || !strings.Contains(out, "auth login acme") {
		t.Fatalf("add: %s %v", out, err)
	}
	if _, err := cli(t, cfg, "plugins", "add", "acme", "--command", "acme-mcp"); err == nil {
		t.Fatal("add replaced without --replace")
	}
	b, _ := os.ReadFile(cfg)
	if !strings.Contains(string(b), "# keep me") || !strings.Contains(string(b), "llm.backend") || !strings.Contains(string(b), "{acme-key}") {
		t.Fatalf("config not edited in place:\n%s", b)
	}
	if out, err := cli(t, cfg, "plugins", "list"); err != nil || !strings.Contains(out, "acme") {
		t.Fatalf("list: %s %v", out, err)
	}
	if _, err := cli(t, cfg, "plugins", "remove", "acme"); err != nil {
		t.Fatal(err)
	}
	if _, err := cli(t, cfg, "plugins", "remove", "acme"); err == nil {
		t.Fatal("removed twice")
	}
}

func TestRoutineDeleteClearsSettings(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	withNotesServer(t, cfg)
	src := filepath.Join(t.TempDir(), "x.prompt")
	_ = os.WriteFile(src, []byte("---\nautomator.outcome: {done: It did it., nothingToDo: []}\nautomator.servers: [notes]\nautomator.schedule: \"0 9 * * *\"\n---\nhi {{> memories}}\n"), 0o600)
	if out, err := cli(t, cfg, "routines", "create", "x", "--from", src); err != nil || !strings.Contains(out, "paused") {
		t.Fatalf("create: %s %v", out, err)
	}
	if out, _ := cli(t, cfg, "settings", "get", "routines.x.enabled"); !strings.Contains(out, "false") {
		t.Fatalf("not disabled: %s", out)
	}
	if _, err := cli(t, cfg, "routines", "delete", "x"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(cfg); strings.Contains(string(b), "routines.x.") {
		t.Fatalf("settings left behind:\n%s", b)
	}
}

func TestPluginNamesAndFolders(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	for _, bad := range []string{"..", ".", "Foo", "a.b", "a/b"} {
		if _, err := cli(t, cfg, "plugins", "add", bad, "--command", "x"); err == nil || !strings.Contains(err.Error(), "can't name a plugin") {
			t.Errorf("plugin name %q accepted: %v", bad, err)
		}
	}
	a, err := loadTest(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	for dir, want := range map[string]bool{
		filepath.Join(a.Layout.Plugins(), "fx"):       true,
		filepath.Join(a.Layout.Plugins(), "fx") + "/": true,
		filepath.Join(a.Layout.Plugins(), ".."):       false,
		a.Layout.Plugins() + "/../x":                  false,
		a.Layout.Plugins():                            false,
	} {
		if got := managed(a, pluginapi.ServerSpec{Dir: dir}); got != want {
			t.Errorf("managed(%s) = %v", dir, got)
		}
	}
}

// Removing a server forgets only the secrets no other server references.
func TestRemoveKeepsSharedSecrets(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	remote := signInServer(t)
	for _, n := range []string{"one", "two"} {
		if _, err := cli(t, cfg, "plugins", "add", n, "--url", remote, "--auth", "headers",
			"--header", "Authorization=Bearer {shared-token}", "--header", "X-Own={"+n+"-key}"); err != nil {
			t.Fatal(err)
		}
	}
	for _, k := range []string{"shared-token", "one-key", "two-key"} {
		_ = testSecrets.Set(k, "v")
	}
	if _, err := cli(t, cfg, "plugins", "remove", "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := testSecrets.Get("shared-token"); err != nil {
		t.Error("removing one signed two out (shared key deleted)")
	}
	if _, err := testSecrets.Get("one-key"); err == nil {
		t.Error("one's own key kept")
	}
	if _, err := testSecrets.Get("two-key"); err != nil {
		t.Error("two's key deleted")
	}
}

// Parallel creates of one routine: one wins, and it stays disabled (the
// losers' cleanup used to reset the winner's setting, leaving it enabled).
func TestParallelCreateStaysDisabled(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	withNotesServer(t, cfg)
	src := "---\nautomator.outcome: {done: It did it., nothingToDo: []}\nautomator.servers: [notes]\nautomator.schedule: \"0 9 * * *\"\n---\nhi {{> memories}}\n"
	var wg sync.WaitGroup
	wins := make(chan bool, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := cli(t, cfg, "routines", "create", "dup", "--source", src)
			wins <- err == nil
		}()
	}
	wg.Wait()
	close(wins)
	n := 0
	for w := range wins {
		if w {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d creates succeeded", n)
	}
	if out, _ := cli(t, cfg, "settings", "get", "routines.dup.enabled"); !strings.Contains(out, "false") {
		t.Fatalf("the created routine isn't disabled: %s", out)
	}
}

func TestSettingsScope(t *testing.T) {
	a, _ := enginetest.Fixture(t, map[string]string{"r": "queue"}, nil)
	app, _ := cli(t, a.Layout.Config(), "settings", "list", "--scope", "app", "--json")
	routine, _ := cli(t, a.Layout.Config(), "settings", "list", "--scope", "routines", "--json")
	if strings.Contains(app, `"routines.r.`) || !strings.Contains(app, `"llm.backend"`) {
		t.Fatalf("app scope: %s", app)
	}
	if !strings.Contains(routine, `"routines.r.enabled"`) || strings.Contains(routine, `"llm.backend"`) {
		t.Fatalf("routine scope: %s", routine)
	}
}

// withNotesServer declares a "notes" MCP server in cfg (a command that
// doesn't exist: known, but it can't be asked for its tools), for tests whose
// routines use one.
func withNotesServer(t *testing.T, cfg string) {
	t.Helper()
	b, _ := os.ReadFile(cfg)
	if strings.Contains(string(b), "mcpServers:") {
		t.Fatal("the config already declares servers")
	}
	_ = os.MkdirAll(filepath.Dir(cfg), 0o700)
	if err := os.WriteFile(cfg, append(b, "\nmcpServers:\n  notes:\n    command: /nonexistent/notes-server\n"...), 0o600); err != nil {
		t.Fatal(err)
	}
}
