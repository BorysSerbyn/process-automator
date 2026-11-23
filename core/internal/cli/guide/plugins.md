# Adding a plugin

A plugin is an MCP server. `%{command} plugins add <name>` starts it, does the handshake and checks its tools first; nothing is added if that fails.

## Adding one
- `--url <url>`: remote; `--auth oauth` (default), `headers` (`--header 'Authorization=Bearer {key}'`) or `none`
- `--command <absolute path>`: local stdio server; `--arg`, `--env NAME=value`
- `--dir <folder>`: a plugin folder, copied into the app
- `--permission <permission>` (with `--url` or `--command`; repeatable): what the OS must allow it (below), kept as `permissions` in its config entry

## A second account
`%{command} plugins add <name> --account-of <plugin>` adds another account of a plugin (built in or added; one that signs in): the same server under `<name>`, with credentials of its own (`%{command} auth login <name>`: its own tokens, secrets and, for a pre-registered OAuth app, its own client ID and secret). It's a plugin like any other: a routine lists it in `automator.servers`, beside the first one, and names a tool both offer with its server (`guide routines`). `plugins remove <name>` forgets only its credentials. An account of an account isn't taken

## A plugin folder
`plugin.yaml` and the server (`command: ./server` runs from the copy):

%{file:testdata/notes-server/plugin.yaml}

%{table:manifest}

%{file:testdata/notes-server/main.go}

## Permissions
What the OS must allow a server before its tools can work, declared by the plugin, never by a routine: a folder's `plugin.yaml` `permissions`, a server's `--permission` (its config entry's `permissions`), a Go plugin's `pluginapi.Plugin.Permissions`. The notify plugin declares `notifications`.
%{table:permissions}

- A routine or an LLM hook needs the permissions of the plugins in its `automator.servers`, and shows them when it's reviewed; trusting it covers them, so a permission a plugin adds later needs a new review
- The scheduler checks them with the OS on every pass, as the app (from a terminal the OS would answer for the terminal). One that isn't granted keeps what needs it from running and is its problem from the start, not found when its first run fails
- `%{command} permissions list` shows what's needed, by what, and what the scheduler found; `%{command} permissions request <permission>` asks the OS, as the app (macOS asks once; after that it's turned on in System Settings, at the address it prints)
- Only macOS's are checked, and on Linux `input-monitoring` (read access to `/dev/input`); the rest show as not checked on this OS, never as missing

## Rules
- A folder's tools must all declare `readOnlyHint`: `true` runs in dry runs, `false` is a write (intercepted)
- Secrets are `{key}` in env or headers, entered by the person with `%{command} auth login <name>`, never in config or chat

## Then
`%{command} plugins check <name>`, `%{command} mcp tools <name>`, then list `<name>` in a routine's `automator.servers` and its tools in `tools`. Go plugins compiled into an app (`core/pluginapi`) need a rebuild: see the engine's repository.
