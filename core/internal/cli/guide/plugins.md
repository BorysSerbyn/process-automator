# Adding a plugin

A plugin is an MCP server. `%{command} plugins add <name>` starts it, does the handshake and checks its tools first; nothing is added if that fails.

## Adding one
- `--url <url>`: remote; `--auth oauth` (default), `headers` (`--header 'Authorization=Bearer {key}'`) or `none`
- `--command <absolute path>`: local stdio server; `--arg`, `--env NAME=value`
- `--dir <folder>`: a plugin folder, copied into the app

## A plugin folder
`plugin.yaml` and the server (`command: ./server` runs from the copy):

%{file:testdata/notes-server/plugin.yaml}

%{table:manifest}

%{file:testdata/notes-server/main.go}

## Rules
- A folder's tools must all declare `readOnlyHint`: `true` runs in dry runs, `false` is a write (intercepted)
- Secrets are `{key}` in env or headers, entered by the person with `%{command} auth login <name>`, never in config or chat

## Then
`%{command} plugins check <name>`, `%{command} mcp tools <name>`, then list `<name>` in a routine's `automator.servers` and its tools in `tools`. Go plugins compiled into an app (`core/pluginapi`) need a rebuild: see the engine's repository.
