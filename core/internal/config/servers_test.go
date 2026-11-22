package config

import (
	"errors"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

func serve(pluginapi.Host) (*sdk.Server, error) { return nil, nil }

func TestValidatePlugins(t *testing.T) {
	ext := pluginapi.Plugin{Name: "tracker", External: &pluginapi.ServerSpec{URL: "https://x"}}
	inproc := pluginapi.Plugin{Name: "notes", InProcess: &pluginapi.InProcess{Serve: serve, Secrets: []pluginapi.SecretSpec{{Name: "api-key", Prompt: "Token"}}}}
	if err := ValidatePlugins([]pluginapi.Plugin{ext, inproc}); err != nil {
		t.Fatal(err)
	}
	bad := map[string]pluginapi.Plugin{
		"duplicate":       ext,
		"both kinds":      {Name: "both", External: ext.External, InProcess: inproc.InProcess},
		"neither kind":    {Name: "none"},
		"no serve":        {Name: "x", InProcess: &pluginapi.InProcess{}},
		"bad name":        {Name: "Bad Name", External: ext.External},
		"bad spec":        {Name: "spec", External: &pluginapi.ServerSpec{Command: "x", URL: "https://y"}},
		"bad secret name": {Name: "y", InProcess: &pluginapi.InProcess{Serve: serve, Secrets: []pluginapi.SecretSpec{{Name: "API KEY"}}}},
	}
	for what, p := range bad {
		set := []pluginapi.Plugin{p}
		if what == "duplicate" {
			set = []pluginapi.Plugin{ext, ext}
		}
		if ValidatePlugins(set) == nil {
			t.Errorf("%s accepted", what)
		}
	}
}

func TestValidateServer(t *testing.T) {
	ok := map[string]pluginapi.ServerSpec{
		"local":                 {Command: "srv"},
		"local with secret env": {Command: "srv", Env: map[string]string{"TOKEN": "{srv-token}", "MODE": "fast"}, Pairing: "approve it"},
		"oauth (default)":       {URL: "https://x", Scopes: []string{"read"}},
		"oauth client":          {URL: "https://x", OAuthClient: &pluginapi.OAuthClient{ClientIDKey: "id", RedirectPort: 8976}},
		"api key header":        {URL: "https://x", Auth: pluginapi.AuthHeaders, Headers: map[string]string{"Authorization": "Bearer {x-key}"}},
		"public, static header": {URL: "https://x", Auth: pluginapi.AuthNone, Headers: map[string]string{"X-Workspace": "acme"}},
		"sse with key":          {URL: "https://x/sse", Transport: pluginapi.TransportSSE, Auth: pluginapi.AuthHeaders, Headers: map[string]string{"X-Key": "{k}"}},
	}
	for what, s := range ok {
		if err := ValidateServer("s", s); err != nil {
			t.Errorf("%s rejected: %v", what, err)
		}
	}
	bad := map[string]pluginapi.ServerSpec{
		"neither":                {},
		"both":                   {Command: "srv", URL: "https://x"},
		"headers on local":       {Command: "srv", Headers: map[string]string{"A": "b"}},
		"env on remote":          {URL: "https://x", Env: map[string]string{"A": "b"}},
		"pairing without secret": {Command: "srv", Pairing: "approve"},
		"unknown auth":           {URL: "https://x", Auth: "basic"},
		"unknown transport":      {URL: "https://x", Transport: "ws"},
		"oauth over sse":         {URL: "https://x", Transport: pluginapi.TransportSSE},
		"headers auth, no ref":   {URL: "https://x", Auth: pluginapi.AuthHeaders, Headers: map[string]string{"A": "plain"}},
		"none auth with secret":  {URL: "https://x", Auth: pluginapi.AuthNone, Headers: map[string]string{"A": "{k}"}},
		"scopes without oauth":   {URL: "https://x", Auth: pluginapi.AuthNone, Scopes: []string{"read"}},
		"oauth client no port":   {URL: "https://x", OAuthClient: &pluginapi.OAuthClient{ClientIDKey: "id"}},
	}
	for what, s := range bad {
		if ValidateServer("s", s) == nil {
			t.Errorf("%s accepted", what)
		}
	}
}

func TestSecretKeysAndExpansion(t *testing.T) {
	s := pluginapi.ServerSpec{URL: "https://x", Auth: pluginapi.AuthHeaders, Headers: map[string]string{
		"X-Org":         "{org-id}",
		"Authorization": "Bearer {api-key}",
		"X-Also":        "{api-key}-{org-id}",
	}}
	if got := strings.Join(SecretKeys(s), ","); got != "api-key,org-id" {
		t.Fatalf("SecretKeys = %s", got)
	}
	vals := map[string]string{"api-key": "sk-1"}
	get := func(k string) (string, error) {
		if v, ok := vals[k]; ok {
			return v, nil
		}
		return "", errors.New("missing " + k)
	}
	if v, err := ExpandSecrets("Bearer {api-key}", get); err != nil || v != "Bearer sk-1" {
		t.Fatalf("expand = %q, %v", v, err)
	}
	if _, err := ExpandSecrets("{org-id}", get); err == nil {
		t.Fatal("missing secret not reported")
	}
	if AuthMode(s) != pluginapi.AuthHeaders || !OAuth(pluginapi.ServerSpec{URL: "https://x"}) || AuthMode(pluginapi.ServerSpec{Command: "c"}) != "" {
		t.Fatal("AuthMode defaults wrong")
	}
}
