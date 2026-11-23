package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestPluginAccounts(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	remote := signInServer(t)
	if out, err := cli(t, cfg, "plugins", "add", "acme", "--url", remote, "--auth", "headers", "--header", "Authorization=Bearer {acme-key}"); err != nil {
		t.Fatalf("add: %s %v", out, err)
	}
	if _, err := cli(t, cfg, "plugins", "add", "acme-two", "--account-of", "acme", "--url", remote); err == nil {
		t.Fatal("--account-of combined with --url")
	}
	out, err := cli(t, cfg, "plugins", "add", "acme-two", "--account-of", "acme")
	if err != nil || !strings.Contains(out, "auth login acme-two") {
		t.Fatalf("add an account: %s %v", out, err)
	}
	if _, err := cli(t, cfg, "plugins", "add", "acme-three", "--account-of", "acme-two"); err == nil {
		t.Fatal("an account of an account was added")
	}
	if _, err := cli(t, cfg, "plugins", "add", "acme-two", "--command", "x"); err == nil {
		t.Fatal("a server was added under an account's name")
	}

	out, _ = cli(t, cfg, "plugins", "list", "--json")
	var rows []PluginInfo
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatal(out, err)
	}
	accounts := map[string]string{}
	for _, r := range rows {
		accounts[r.Name] = r.AccountOf
	}
	if a, ok := accounts["acme-two"]; !ok || a != "acme" || accounts["acme"] != "" {
		t.Fatalf("plugins list: %s", out)
	}
	out, _ = cli(t, cfg, "auth", "status")
	if !strings.Contains(out, "auth login acme-two) (an account of acme)") {
		t.Fatalf("auth status:\n%s", out)
	}

	if out, err := cli(t, cfg, "plugins", "remove", "acme"); err == nil || !strings.Contains(err.Error(), "acme-two") {
		t.Fatalf("removed a server with accounts: %s %v", out, err)
	}
	if out, err := cli(t, cfg, "plugins", "remove", "acme-two"); err != nil || !strings.Contains(out, "an account of acme") {
		t.Fatalf("remove the account: %s %v", out, err)
	}
	if _, err := cli(t, cfg, "plugins", "remove", "acme"); err != nil {
		t.Fatal(err)
	}
}
