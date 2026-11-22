# Adding a plugin

Routines get their tools from plugins, and a plugin is an MCP server: an existing one, added as it is, or one you write and install as a plugin folder. Adding one needs no change to the app.

## Three ways to add one
`%{command} plugins add <name>` adds a server under a name routines use (lowercase letters, digits and dashes):
- **A remote server**: `--url <url>`, signing in with `--auth oauth` (the default: `auth login` opens the browser), `--auth headers` (an API key in a header: `--header 'Authorization=Bearer {acme-key}'`) or `--auth none` (public)
- **A local command**, an MCP server over stdio: `--command <absolute path or command on PATH>`, with `--arg` for each argument and `--env NAME=value` for its environment
- **A plugin folder**: `--dir <folder>`, a server you wrote, with a `plugin.yaml` (below)

Its flags:

%{table:add-flags}

## Writing your own
Any language works; the server speaks MCP over stdio. In Go, the official MCP Go SDK (`github.com/modelcontextprotocol/go-sdk`, which the app uses itself) makes it short. This server keeps notes in a file, with one read-only tool and one write:

%{file:testdata/notes-server/main.go}

Every tool must declare its annotations, with `readOnlyHint`: `true` for a tool that only reads (dry runs call it for real), `false` for one that changes anything (dry runs intercept it, and trusting a routine lists it as a write). Build it in its folder (`go build -o server .`), next to its `plugin.yaml`.

## The plugin folder
A folder with a `plugin.yaml` that says how to start its server:

%{file:testdata/notes-server/plugin.yaml}

%{table:manifest}

Unknown fields are an error. `plugins add <name> --dir <folder>` copies the folder into the app, so where it was written doesn't matter: the server runs from the copy, with the copy as its working directory, over stdio. `--replace` installs a new version (the old one stays if the new one fails its checks); `plugins remove <name>` deletes the copy.

## What plugins add checks
Before anything is added, `plugins add` starts the server, does the MCP handshake and lists its tools: each needs an object input schema, a name used once, and at least one tool must be offered. A plugin folder's tools must all declare annotations (`readOnlyHint`); other servers' undeclared ones are a warning, and treated as writes. If a check fails, nothing is added. A server that needs credentials is checked as far as it can be without them.

This runs the server's code: add only servers you trust.

## Secrets
Secrets never go in `plugin.yaml`, the config or a chat. Reference each as `{key}` in an env value (`--env`, or the folder's `env`) or a header (`--header`), and the person enters it with `%{command} auth login <name>`, which keeps it in the system's secret store (over MCP, `auth_login` opens a page in their browser). A local server that issues its own credential on first contact (an approval dialog, then a key shown once) sets `pairing`: `auth login` starts it first so it can ask, then asks for the key it showed.

## Then
1. `%{command} auth login <name>`, if it needs a sign-in or secrets
2. `%{command} plugins check <name>`: starts it and lists its tools, counted as read-only or writes
3. `%{command} mcp tools <name>`: its tools, by the names a routine allows (`--json` marks the read-only ones)
4. In a routine: `<name>` in `automator.servers`, and the tools it may call in `tools` (`%{command} guide routines`)

## Plugins compiled into the app
Apps built on the engine can also compile plugins into their binary, in Go, with its public `core/pluginapi` package. That means rebuilding the app, and it's described in the engine's repository; everything above works without it.
