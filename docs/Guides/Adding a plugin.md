Back to [[Home]]. See also [[Guides/Plugins and sign-ins|Plugins and sign-ins]], [[Guides/Writing a routine|Writing a routine]].

A plugin is any MCP server. Add it with `procauto plugins add <name>` (`--url` for a remote server, `--command` for a local one, `--dir` for a folder; see `plugins add --help`), or by hand under `mcpServers` in the config file (`procauto paths` shows where). Then list it in a routine's `automator.servers` and allow its tools in `tools:`. No code needed. `procauto plugins remove <name>` takes it out again and forgets its credentials.

```sh
procauto plugins add github --url https://api.githubcopilot.com/mcp/
procauto plugins add example --url https://example.com/mcp --auth headers --header 'Authorization=Bearer {example-key}'
procauto plugins add files --command npx --arg -y --arg @modelcontextprotocol/server-filesystem --arg ~/notes
procauto plugins add weather --dir ./weather-mcp
```

`plugins add` tries the server first (starts it, does the MCP handshake, lists its tools) and adds nothing if that fails. A server that needs credentials is tried as far as it can be: sign in with `auth login <name>`, then `plugins check <name>` tries it fully. `plugins check` with no name tries every plugin. `--replace` replaces a server of the same name. Names are lowercase letters, digits and dashes; `automator` and `notify` are taken by the built-ins.

## Plugin folder (your own server)
A server you wrote (or Claude Code wrote) can be installed as a folder: `procauto plugins add weather --dir ./weather-mcp`. The folder is copied into the app, so it doesn't matter where it was written; `--replace` installs a new version (the old one stays if the new one fails its trial), and `plugins remove` deletes the copy. It needs a `plugin.yaml`:

```yaml
description: Weather forecasts for my routines
command: ./server          # relative paths resolve inside the folder; or e.g. python3 / npx
args: [--stdio]
env:
  WEATHER_KEY: "{weather-key}"   # asked for by `auth login weather`
```

Every tool must declare its annotations, with `readOnlyHint` (true for reads, false for writes): dry runs run read-only tools and intercept the rest, so a folder plugin with an undeclared tool is rejected.

Secrets never go in the config: reference them as `{secret-key}` in `env` or `headers`, and `procauto auth login <name>` asks for each one and keeps it in the Keychain (on Linux, the Secret Service keyring).

## Local command (stdio)

```yaml
mcpServers:
  notes:
    command: /usr/local/bin/notes-mcp   # an absolute path, or a command on your PATH
    args: [--read-only]
    env:
      NOTES_TOKEN: "{notes-token}"   # asked for by `auth login notes`
      NOTES_MODE: fast               # plain values are fine too
```

Local apps that approve clients themselves and then show a credential once also set `pairing: "<what to click>"`: `auth login` connects first so the app can ask, then asks you to paste the credential.

## Remote server

```yaml
mcpServers:
  # OAuth (the default): `auth login` opens the browser; tokens are refreshed automatically.
  tracker:
    url: https://example.com/mcp
    scopes: [read]                  # optional

  # OAuth for servers without automatic client registration: use your own app's credentials.
  # `auth login` asks for both keys the first time; the app must allow http://localhost:<redirectPort>/callback.
  chat:
    url: https://chat.example.com/mcp
    oauthClient: {clientIdKey: chat-client-id, clientSecretKey: chat-client-secret, redirectPort: 8976}

  # API key in a header.
  wiki:
    url: https://wiki.example.com/mcp
    auth: headers
    headers:
      Authorization: "Bearer {wiki-api-key}"

  # Public server (no sign-in), optionally with plain headers.
  public:
    url: https://public.example.com/mcp
    auth: none

  # Older servers that use the HTTP+SSE transport (headers or none auth).
  legacy:
    url: https://legacy.example.com/sse
    transport: sse
    auth: headers
    headers: {X-API-Key: "{legacy-key}"}
```

## Check it

```sh
procauto auth login <name>     # if it needs secrets or a sign-in
procauto auth status
procauto plugins check <name>  # its tools, counted as read-only or write
procauto mcp tools <name>      # the tools you can allow in a routine (--json marks readOnly)
```

Tools a server doesn't mark read-only are treated as writes: dry runs intercept them.

Unknown or misspelled fields are an error, so a typo shows up immediately instead of being ignored.
