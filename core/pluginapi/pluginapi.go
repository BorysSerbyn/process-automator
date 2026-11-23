// Package pluginapi is how an app compiles integrations into the engine. A
// plugin is a named MCP server that routines list in automator.servers: an
// external one the app presets (External: a remote URL or a local command),
// or Go code that builds one in-process (InProcess), which gets a Host from
// the engine. It is a leaf package: it depends on the MCP SDK and nothing
// else in the engine.
package pluginapi

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Plugin is one integration. Set exactly one of External or InProcess.
type Plugin struct {
	// Name is what routines use (automator.servers) and the plugin's config
	// key: lowercase letters, digits and dashes.
	Name        string
	Description string
	// Prepare is what I do outside the app before signing in to it the first
	// time (e.g. create a Slack app), as numbered steps in plain words: shown,
	// and confirmed, before any credential is asked for. Web addresses and
	// Markdown links, [text](https://…), show as links (in the terminal, as the
	// text and its address), so a long prefilled address can hide behind a few
	// words.
	Prepare string
	// Permissions are what the OS must allow the plugin before its tools
	// can work, by name: input-monitoring, accessibility, screen-recording,
	// notifications, full-disk-access, calendars, contacts, reminders,
	// microphone, camera, automation. Routines that use it need them too:
	// trusting one covers them, and while one isn't granted it doesn't run.
	// For External, a config mcpServers.<name> entry that replaces the
	// preset declares its own.
	Permissions []string

	// External presets an external MCP server. The config file's
	// mcpServers.<name> entry, if present, replaces it.
	External *ServerSpec
	// InProcess is a server built by Go code compiled into the app.
	InProcess *InProcess
}

// InProcess is an in-process plugin: Go code that builds an MCP server.
type InProcess struct {
	// Serve builds the server. It runs once per session, so keep it cheap.
	// Tools must declare readOnlyHint accurately: dry-run intercepts every
	// tool that isn't read-only.
	Serve func(h Host) (*sdk.Server, error)
	// Secrets it needs (e.g. an API key). `auth login <name>` asks for each;
	// Host.Secret reads them.
	Secrets []SecretSpec
	// RoutineSection says it reads a section from the routines that use it
	// (Host.RoutineConfig). Routines may only write automator.plugins.<name>
	// for plugins that do.
	RoutineSection bool
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
	// Config decodes this plugin's config section (plugins.<name>) into v by
	// its yaml tags, keeping v's values for absent fields. Unknown fields are
	// an error.
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
	// Run is the routine run this session serves; zero when there's none.
	Run() Run
	// AskToAcknowledge hands n to the app as one of its own notifications,
	// about the run this session serves, that waits for me to acknowledge
	// it: the app's frontends show it with an Acknowledge button and list it
	// until I do, and the app's health is error meanwhile. It returns the
	// notification's ID (`notifications ack <id>`).
	AskToAcknowledge(n Notice) (id string, err error)
}

// Notice is a notification a plugin asks the app to show
// (Host.AskToAcknowledge).
type Notice struct {
	Title, Body string
	// Urgent breaks through focus modes where the OS allows (macOS
	// time-sensitive, Linux critical urgency).
	Urgent bool
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

// Auth modes and transports (ServerSpec.Auth, ServerSpec.Transport).
const (
	AuthOAuth   = "oauth"
	AuthHeaders = "headers"
	AuthNone    = "none"

	TransportStreamable = "streamable"
	TransportSSE        = "sse"
)
