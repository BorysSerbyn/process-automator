package pluginapi

import (
	"errors"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func serve(Host) (*sdk.Server, error) { return nil, nil }

func TestValidate(t *testing.T) {
	ext := Plugin{Name: "tracker", External: &ServerSpec{URL: "https://x"}}
	inproc := Plugin{Name: "notes", Serve: serve, Secrets: []SecretSpec{{Name: "api-key", Prompt: "Token"}}}
	if err := Validate([]Plugin{ext, inproc}); err != nil {
		t.Fatal(err)
	}
	bad := map[string]Plugin{
		"duplicate":        ext,
		"both kinds":       {Name: "both", External: ext.External, Serve: serve},
		"neither kind":     {Name: "none"},
		"bad name":         {Name: "Bad Name", External: ext.External},
		"bad spec":         {Name: "spec", External: &ServerSpec{Command: "x", URL: "https://y"}},
		"external secrets": {Name: "x", External: ext.External, Secrets: inproc.Secrets},
		"bad secret name":  {Name: "y", Serve: serve, Secrets: []SecretSpec{{Name: "API KEY"}}},
	}
	for what, p := range bad {
		set := []Plugin{p}
		if what == "duplicate" {
			set = []Plugin{ext, ext}
		}
		if Validate(set) == nil {
			t.Errorf("%s accepted", what)
		}
	}
}

func TestSecretKeyIsNamespaced(t *testing.T) {
	if got := SecretKey("notes", "api-key"); got != "plugin/notes/api-key" {
		t.Fatal(got)
	}
}

func TestServerSpecValidate(t *testing.T) {
	ok := map[string]ServerSpec{
		"local":                 {Command: "srv"},
		"local with secret env": {Command: "srv", Env: map[string]string{"TOKEN": "{srv-token}", "MODE": "fast"}, Pairing: "approve it"},
		"oauth (default)":       {URL: "https://x", Scopes: []string{"read"}},
		"oauth client":          {URL: "https://x", OAuthClient: &OAuthClient{ClientIDKey: "id", RedirectPort: 8976}},
		"api key header":        {URL: "https://x", Auth: AuthHeaders, Headers: map[string]string{"Authorization": "Bearer {x-key}"}},
		"public, static header": {URL: "https://x", Auth: AuthNone, Headers: map[string]string{"X-Workspace": "acme"}},
		"sse with key":          {URL: "https://x/sse", Transport: TransportSSE, Auth: AuthHeaders, Headers: map[string]string{"X-Key": "{k}"}},
	}
	for what, s := range ok {
		if err := s.Validate("s"); err != nil {
			t.Errorf("%s rejected: %v", what, err)
		}
	}
	bad := map[string]ServerSpec{
		"neither":                {},
		"both":                   {Command: "srv", URL: "https://x"},
		"headers on local":       {Command: "srv", Headers: map[string]string{"A": "b"}},
		"env on remote":          {URL: "https://x", Env: map[string]string{"A": "b"}},
		"pairing without secret": {Command: "srv", Pairing: "approve"},
		"unknown auth":           {URL: "https://x", Auth: "basic"},
		"unknown transport":      {URL: "https://x", Transport: "ws"},
		"oauth over sse":         {URL: "https://x", Transport: TransportSSE},
		"headers auth, no ref":   {URL: "https://x", Auth: AuthHeaders, Headers: map[string]string{"A": "plain"}},
		"none auth with secret":  {URL: "https://x", Auth: AuthNone, Headers: map[string]string{"A": "{k}"}},
		"scopes without oauth":   {URL: "https://x", Auth: AuthNone, Scopes: []string{"read"}},
		"oauth client no port":   {URL: "https://x", OAuthClient: &OAuthClient{ClientIDKey: "id"}},
	}
	for what, s := range bad {
		if s.Validate("s") == nil {
			t.Errorf("%s accepted", what)
		}
	}
}

func TestSecretKeysAndExpansion(t *testing.T) {
	s := ServerSpec{URL: "https://x", Auth: AuthHeaders, Headers: map[string]string{
		"X-Org":         "{org-id}",
		"Authorization": "Bearer {api-key}",
		"X-Also":        "{api-key}-{org-id}",
	}}
	if got := strings.Join(s.SecretKeys(), ","); got != "api-key,org-id" {
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
	if s.AuthMode() != AuthHeaders || (ServerSpec{URL: "https://x"}).AuthMode() != AuthOAuth || (ServerSpec{Command: "c"}).AuthMode() != "" {
		t.Fatal("AuthMode defaults wrong")
	}
}
