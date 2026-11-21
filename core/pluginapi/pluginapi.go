// Package pluginapi is the contract between the engine and plugins, and the
// engine's only view of integrations. It is a leaf package: it depends on the
// MCP SDK and nothing else in the engine.
//
// A plugin is a named MCP server that routines list in automator.servers. It is
// either external (a remote URL or a local command, see ServerSpec) or in-process
// (Go code that builds an MCP server, see Plugin.Serve).
package pluginapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.yaml.in/yaml/v3"
)

// Plugin is one integration. Set exactly one of External or Serve.
type Plugin struct {
	// Name is what routines use (automator.servers) and the plugin's config key.
	Name        string
	Description string

	// External describes an external MCP server. The config file's
	// mcpServers.<name> entry, if present, replaces it.
	External *ServerSpec

	// Serve builds an in-process MCP server. It runs once per session, so keep it
	// cheap. Tools must declare readOnlyHint accurately: dry-run intercepts every
	// tool that isn't read-only.
	Serve func(h Host) (*sdk.Server, error)
	// Secrets an in-process plugin needs (e.g. an API key). `auth login <name>`
	// asks for each; Host.Secret reads them.
	Secrets []SecretSpec
	// RoutineSection says the plugin reads a section from the routines that
	// use it (Host.RoutineConfig). Routines may only write automator.plugins.<name>
	// for plugins that do.
	RoutineSection bool
	// Prepare is what I do outside the app before signing in to it the first
	// time (e.g. create a Slack app), as numbered steps in plain words: shown,
	// and confirmed, before any credential is asked for. Web addresses and
	// Markdown links, [text](https://…), show as links (in the terminal, as the
	// text and its address), so a long prefilled address can hide behind a few
	// words.
	Prepare string
	// Uses names the other plugins it connects to through Host.Connect (as its
	// config says), so whatever a routine using it needs from those (a
	// sign-in) counts as the routine's too.
	Uses func(h Host) []string
}

// SecretSpec declares a secret an in-process plugin needs.
type SecretSpec struct {
	Name   string // key within the plugin, e.g. "api-key"
	Prompt string // shown when asking for it, e.g. "Notion integration token"
}

// Host is what in-process plugins get from the engine.
type Host interface {
	// Connect opens a session to another plugin by name (e.g. a plugin that
	// reads through a local app's MCP server). The caller closes it.
	Connect(ctx context.Context, name string) (*sdk.ClientSession, error)
	// ConnectApp opens a session to this app's own MCP server, read-only (`mcp
	// serve --read-only` on the app's config: its read-only tools, such as
	// routines_list and runs), for a plugin that needs the app's routines or
	// runs. The app's server isn't a plugin, and plugins never read the app's
	// files: this is their way in. The caller closes it.
	ConnectApp(ctx context.Context) (*sdk.ClientSession, error)
	// AppName is the name of the app the plugin runs in (e.g. "Process
	// Automator"), for what the plugin shows as the app's, such as a desktop
	// notification's sender.
	AppName() string
	// Config decodes this plugin's config section (plugins.<name>) into v, keeping
	// v's values for absent fields. Unknown fields are an error. Like
	// RoutineConfig, it decodes by yaml tags.
	Config(v any) error
	// Secret returns one of this plugin's declared secrets.
	Secret(name string) (string, error)
	// DataDir is this plugin's own folder in the app's data folder (created
	// on first use), for what it keeps between runs, such as caches. Plugins
	// never pick their own paths.
	DataDir() (string, error)
	// RoutineConfig decodes the running routine's section for this plugin
	// (automator.plugins.<name> in the routine file) into v by its yaml tags,
	// keeping v's values when there's none, or no routine (e.g. `procauto
	// mcp tools`). Unknown fields are an error. Only plugins with
	// RoutineSection set get one.
	RoutineConfig(v any) error
	// Run is the routine run this session serves (its Plugins left out:
	// RoutineConfig reads this plugin's section); zero when there's none.
	Run() Run
}

// Run is the routine run a plugin session serves. It's zero outside a run
// (e.g. `procauto mcp tools`, `plugins check`) and in sessions a plugin
// opens to another plugin (Host.Connect).
type Run struct {
	// Routine is the routine's name.
	Routine string
	// ID is the run's history ID; "" when the routine is being checked
	// rather than run (doctor, routines check, routines trust).
	ID string
	// Folder is the routine's folder (automator.folder); "" when it has none.
	Folder string
	// Plugins are the routine's automator.plugins sections, by plugin name.
	Plugins map[string]any
}

// ServerSpec is an external MCP server: a local stdio command or a remote URL.
// It's also the shape of mcpServers.<name> in the config file.
//
// Secrets are never written in a spec: values in Env and Headers can reference
// stored secrets as {secret-key} (e.g. "Bearer {notion-api-key}"). `auth login`
// asks for every referenced secret, so any server can be signed in to without
// code.
type ServerSpec struct {
	// Description says what the server is for (shown by `plugins list`).
	Description string `yaml:"description,omitempty"`

	// Command and Args start a local stdio server.
	Command string   `yaml:"command,omitempty"`
	Args    []string `yaml:"args,omitempty"`
	// Dir is the command's working directory (a plugin folder's copy).
	Dir string `yaml:"dir,omitempty"`
	// Env is added to the command's environment; values may reference secrets.
	Env map[string]string `yaml:"env,omitempty"`
	// Pairing is set for local servers that issue their own credential on first
	// contact (e.g. a local app that shows an approval dialog, then the credential once).
	// `auth login` then connects without the secrets first, showing this hint,
	// before asking for them.
	Pairing string `yaml:"pairing,omitempty"`

	// URL is a remote server.
	URL string `yaml:"url,omitempty"`
	// Transport is "streamable" (default, streamable HTTP) or "sse" (the older
	// HTTP+SSE transport).
	Transport string `yaml:"transport,omitempty"`
	// Auth is how a remote server is signed in to: "oauth" (default; browser
	// sign-in, tokens kept and refreshed), "headers" (secrets referenced in
	// Headers, e.g. an API key) or "none" (a public server).
	Auth string `yaml:"auth,omitempty"`
	// Headers are sent with every request; values may reference secrets.
	Headers map[string]string `yaml:"headers,omitempty"`
	// Scopes limits the OAuth scopes requested.
	Scopes []string `yaml:"scopes,omitempty"`
	// OAuthClient is set for OAuth servers that don't support dynamic client
	// registration: the app's client credentials live in the secrets store.
	OAuthClient *OAuthClient `yaml:"oauthClient,omitempty"`
}

// OAuthClient is a pre-registered OAuth client.
type OAuthClient struct {
	// ClientIDKey and ClientSecretKey are secret-store keys (asked for on first login).
	ClientIDKey     string `yaml:"clientIdKey"`
	ClientSecretKey string `yaml:"clientSecretKey,omitempty"`
	// RedirectPort is the fixed loopback port; the app registration must list
	// http://localhost:<port>/callback as a redirect URL.
	RedirectPort int `yaml:"redirectPort"`
}

// Auth modes and transports.
const (
	AuthOAuth   = "oauth"
	AuthHeaders = "headers"
	AuthNone    = "none"

	TransportStreamable = "streamable"
	TransportSSE        = "sse"
)

// Remote reports whether the server is reached over HTTP.
func (s ServerSpec) Remote() bool { return s.URL != "" }

// AuthMode is the remote server's effective auth mode ("" for local servers).
func (s ServerSpec) AuthMode() string {
	switch {
	case !s.Remote():
		return ""
	case s.Auth == "":
		return AuthOAuth
	default:
		return s.Auth
	}
}

var secretRef = regexp.MustCompile(`\{([a-z0-9][a-z0-9/-]*)\}`)

// SecretKeys lists the stored secrets the server needs, in a stable order:
// those referenced in Env or Headers (OAuth tokens and client credentials are
// handled by the OAuth sign-in).
func (s ServerSpec) SecretKeys() []string {
	seen := map[string]bool{}
	var keys []string
	for _, m := range []map[string]string{s.Env, s.Headers} {
		names := make([]string, 0, len(m))
		for k := range m {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			for _, ref := range secretRef.FindAllStringSubmatch(m[k], -1) {
				if !seen[ref[1]] {
					seen[ref[1]] = true
					keys = append(keys, ref[1])
				}
			}
		}
	}
	return keys
}

// ExpandSecrets replaces {secret-key} references in v with values from get.
func ExpandSecrets(v string, get func(key string) (string, error)) (string, error) {
	var firstErr error
	out := secretRef.ReplaceAllStringFunc(v, func(ref string) string {
		val, err := get(ref[1 : len(ref)-1])
		if err != nil && firstErr == nil {
			firstErr = err
		}
		return val
	})
	return out, firstErr
}

// Validate checks a server spec.
func (s ServerSpec) Validate(name string) error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("server %q: "+format, append([]any{name}, args...)...)
	}
	if (s.Command == "") == (s.URL == "") {
		return bad("set exactly one of command or url")
	}
	if !s.Remote() {
		if s.Transport != "" || s.Auth != "" || len(s.Headers) > 0 || len(s.Scopes) > 0 || s.OAuthClient != nil {
			return bad("transport, auth, headers, scopes and oauthClient are for url servers")
		}
		if s.Pairing != "" && len(s.SecretKeys()) == 0 {
			return bad("pairing needs a secret referenced in env")
		}
		return nil
	}
	if len(s.Args) > 0 || len(s.Env) > 0 || s.Pairing != "" || s.Dir != "" {
		return bad("args, env, dir and pairing are for command servers")
	}
	switch s.Transport {
	case "", TransportStreamable, TransportSSE:
	default:
		return bad("transport must be streamable or sse")
	}
	refs := len(s.SecretKeys()) > 0
	switch s.AuthMode() {
	case AuthOAuth:
		if s.Transport == TransportSSE {
			return bad("oauth isn't supported over sse; use auth: headers or none")
		}
		if c := s.OAuthClient; c != nil && (c.ClientIDKey == "" || c.RedirectPort <= 0) {
			return bad("oauthClient needs clientIdKey and redirectPort")
		}
	case AuthHeaders:
		if !refs {
			return bad("auth: headers needs a secret referenced in headers, e.g. Authorization: \"Bearer {my-api-key}\"")
		}
	case AuthNone:
		if refs {
			return bad("auth: none can't reference secrets; use auth: headers")
		}
	default:
		return bad("auth must be oauth, headers or none")
	}
	if s.AuthMode() != AuthOAuth && (len(s.Scopes) > 0 || s.OAuthClient != nil) {
		return bad("scopes and oauthClient are for auth: oauth")
	}
	return nil
}

// SecretKey is the secrets-store key of an in-process plugin's secret.
func SecretKey(plugin, name string) string { return "plugin/" + plugin + "/" + name }

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ValidName reports whether name can name a plugin: lowercase letters, digits
// and dashes. Names become config keys, keychain keys and folder names.
func ValidName(name string) bool { return nameRE.MatchString(name) }

// Validate checks a plugin definition.
func (p Plugin) Validate() error {
	if !nameRE.MatchString(p.Name) {
		return fmt.Errorf("plugin %q: names are lowercase letters, digits and dashes", p.Name)
	}
	switch {
	case (p.External == nil) == (p.Serve == nil):
		return fmt.Errorf("plugin %q: set exactly one of External or Serve", p.Name)
	case p.External != nil && len(p.Secrets) > 0:
		return fmt.Errorf("plugin %q: external plugins reference secrets in External (env, headers, oauthClient)", p.Name)
	case p.External != nil:
		return p.External.Validate(p.Name)
	}
	for _, s := range p.Secrets {
		if !nameRE.MatchString(s.Name) {
			return fmt.Errorf("plugin %q: secret %q: names are lowercase letters, digits and dashes", p.Name, s.Name)
		}
	}
	return nil
}

// Validate checks a set of plugins: each well-formed, names unique.
func Validate(ps []Plugin) error {
	seen := map[string]bool{}
	var errs []error
	for _, p := range ps {
		if err := p.Validate(); err != nil {
			errs = append(errs, err)
		}
		if seen[p.Name] {
			errs = append(errs, fmt.Errorf("plugin %q registered twice", p.Name))
		}
		seen[p.Name] = true
	}
	return errors.Join(errs...)
}

// DecodeSection decodes a plugin's section (as read from YAML: a config
// section or a routine's automator.plugins.<name>) into v, by its yaml tags,
// keeping v's values for absent fields. Unknown fields are an error.
func DecodeSection(section, v any) error {
	raw, err := yaml.Marshal(section)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}
