package engine

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BorysSerbyn/process-automator/core/internal/config"
	"github.com/BorysSerbyn/process-automator/core/internal/secrets"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// chatPlugin is an external preset with a pre-registered OAuth app, like a
// chat service without dynamic client registration.
func chatPlugin() pluginapi.Plugin {
	return pluginapi.Plugin{
		Name: "chat", Description: "Chat", Prepare: "1. Make an app", Permissions: []string{"notifications"},
		External: &pluginapi.ServerSpec{URL: "https://chat.example/mcp", Scopes: []string{"search"},
			OAuthClient: &pluginapi.OAuthClient{ClientIDKey: "chat-client-id", ClientSecretKey: "chat-client-secret", RedirectPort: 8976}},
	}
}

func loadAccounts(t *testing.T, yaml string, store secrets.Store, plugins ...pluginapi.Plugin) *App {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := Load(path, plugins, store)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAnAccountOfAnExternalPluginHasItsOwnCredentials(t *testing.T) {
	a := loadAccounts(t, "accounts:\n  chat-work:\n    of: chat\n", secrets.NewMemory(), chatPlugin())
	p, ok := a.FindPlugin("chat-work")
	if !ok || p.External == nil || p.Description != "Chat" || p.Prepare == "" || !slices.Equal(a.ServerPermissions("chat-work"), []string{"notifications"}) {
		t.Fatalf("the account isn't the plugin under its own name: %+v", p)
	}
	if of, ok := a.AccountOf("chat-work"); !ok || of != "chat" {
		t.Fatalf("AccountOf = %q %v", of, ok)
	}
	acct, base := a.Config.MCPServers["chat-work"], a.Config.MCPServers["chat"]
	if acct.URL != base.URL || acct.OAuthClient.RedirectPort != 8976 {
		t.Fatalf("not the same server: %+v", acct)
	}
	if acct.OAuthClient.ClientIDKey == base.OAuthClient.ClientIDKey || acct.OAuthClient.ClientSecretKey == base.OAuthClient.ClientSecretKey {
		t.Fatal("the account reads its plugin's client credentials")
	}
	s, ok := a.FindSignIn("chat-work")
	if !ok || !s.OAuth || s.AccountOf != "chat" || s.Prepare == "" || len(s.Secrets) != 2 {
		t.Fatalf("sign-in: %+v", s)
	}
	baseIn, _ := a.FindSignIn("chat")
	for i := range s.Secrets {
		if s.Secrets[i].Key == baseIn.Secrets[i].Key {
			t.Errorf("secret %s is shared with chat", s.Secrets[i].Key)
		}
	}
}

func TestAnAccountOfAnAddedServerReadsItsOwnHeaderSecrets(t *testing.T) {
	cfg := "mcpServers:\n  acme:\n    url: https://acme.example\n    auth: headers\n    headers:\n      Authorization: Bearer {acme-key}\naccounts:\n  acme-two:\n    of: acme\n"
	a := loadAccounts(t, cfg, secrets.NewMemory())
	got := a.Config.MCPServers["acme-two"].Headers["Authorization"]
	if want := "Bearer {" + config.AccountSecretKey("acme-two", "acme-key") + "}"; got != want {
		t.Fatalf("header = %q, want %q", got, want)
	}
	s, _ := a.FindSignIn("acme-two")
	if len(s.Secrets) != 1 || s.Secrets[0].Label != "acme-key" || s.Secrets[0].Key == "acme-key" {
		t.Fatalf("sign-in secrets: %+v", s.Secrets)
	}
}

func TestAnAccountOfAnInProcessPluginReadsItsOwnSecretAndConfig(t *testing.T) {
	store := secrets.NewMemory()
	_ = store.Set(PluginSecretKey("notes", "api-key"), "first")
	_ = store.Set(PluginSecretKey("notes-two", "api-key"), "second")
	var seen []string
	p := pluginapi.Plugin{Name: "notes", InProcess: &pluginapi.InProcess{
		Secrets: []pluginapi.SecretSpec{{Name: "api-key", Prompt: "Token"}},
		Serve: func(h pluginapi.Host) (*sdk.Server, error) {
			var cfg struct {
				Space string `yaml:"space"`
			}
			if err := h.Config(&cfg); err != nil {
				return nil, err
			}
			tok, err := h.Secret("api-key")
			seen = append(seen, cfg.Space+"="+tok)
			return sdk.NewServer(&sdk.Implementation{Name: "notes"}, nil), err
		},
	}}
	cfg := "plugins:\n  notes:\n    space: home\n  notes-three:\n    space: work\naccounts:\n  notes-two:\n    of: notes\n  notes-three:\n    of: notes\n"
	a := loadAccounts(t, cfg, store, p)
	for _, name := range []string{"notes", "notes-two", "notes-three"} {
		s, err := a.Connector.Connect(context.Background(), name)
		if name == "notes-three" {
			if err == nil {
				t.Fatal("an account with no secret stored connected")
			}
			continue
		}
		if err != nil {
			t.Fatal(name, err)
		}
		_ = s.Close()
	}
	// Its own config section, or its plugin's while it has none.
	if !slices.Equal(seen, []string{"home=first", "home=second", "work="}) {
		t.Fatalf("sessions saw %v", seen)
	}
	if s, ok := a.FindSignIn("notes-two"); !ok || s.Secrets[0].Key != PluginSecretKey("notes-two", "api-key") {
		t.Fatalf("sign-in: %+v", s)
	}
}

func TestAccountsThatCantBe(t *testing.T) {
	for what, cfg := range map[string]string{
		"an account of an account":  "accounts:\n  chat-a:\n    of: chat\n  chat-b:\n    of: chat-a\n",
		"of nothing":                "accounts:\n  chat-a:\n    of: nope\n",
		"a plugin's name":           "accounts:\n  chat:\n    of: chat\n",
		"an added server's name":    "mcpServers:\n  chat-a:\n    url: https://x\naccounts:\n  chat-a:\n    of: chat\n",
		"no plugin":                 "accounts:\n  chat-a: {}\n",
		"a name that can't be used": "accounts:\n  Chat A:\n    of: chat\n",
	} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		_ = os.WriteFile(path, []byte(cfg), 0o600)
		if _, err := Load(path, []pluginapi.Plugin{chatPlugin()}, secrets.NewMemory()); err == nil {
			t.Errorf("%s loaded", what)
		}
	}
}

func TestAddAndRemoveAnAccount(t *testing.T) {
	store := secrets.NewMemory()
	plain := pluginapi.Plugin{Name: "files", External: &pluginapi.ServerSpec{Command: "files-mcp"}}
	a := loadAccounts(t, "", store, chatPlugin(), notesPlugin(), plain)
	for what, add := range map[string][2]string{
		"an account of nothing":           {"x", "nope"},
		"a plugin's name":                 {"notes", "chat"},
		"one of a plugin without sign-in": {"files-two", "files"},
		"a bad name":                      {"Chat", "chat"},
	} {
		if err := a.AddAccount(add[0], add[1]); err == nil {
			t.Errorf("%s added", what)
		}
	}
	if err := a.AddAccount("chat-work", "chat"); err != nil {
		t.Fatal(err)
	}
	a = loadAccounts(t, mustRead(t, a.Layout.Config()), store, chatPlugin(), notesPlugin(), plain)
	if err := a.AddAccount("chat-more", "chat-work"); err == nil || !strings.Contains(err.Error(), "account of chat") {
		t.Fatalf("an account of an account: %v", err)
	}
	if err := a.AddAccount("chat-work", "chat"); err == nil {
		t.Fatal("added twice")
	}
	// Both signed in, each with its own app and tokens.
	acct, _ := a.FindSignIn("chat-work")
	base, _ := a.FindSignIn("chat")
	for _, s := range append(acct.Secrets, base.Secrets...) {
		_ = store.Set(s.Key, "v-"+s.Key)
	}
	_ = store.Set("oauth/chat", "{}")
	_ = store.Set("oauth/chat-work", "{}")

	if err := a.RemoveAccount("chat-work"); err != nil {
		t.Fatal(err)
	}
	for _, s := range acct.Secrets {
		if _, err := store.Get(s.Key); err == nil {
			t.Errorf("the account's %s is still stored", s.Key)
		}
	}
	if _, err := store.Get("oauth/chat-work"); err == nil {
		t.Error("the account's tokens are still stored")
	}
	for _, k := range []string{base.Secrets[0].Key, base.Secrets[1].Key, "oauth/chat"} {
		if _, err := store.Get(k); err != nil {
			t.Errorf("removing the account forgot chat's %s", k)
		}
	}
	a = loadAccounts(t, mustRead(t, a.Layout.Config()), store, chatPlugin(), notesPlugin(), plain)
	if _, ok := a.AccountOf("chat-work"); ok {
		t.Fatal("still an account")
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
