Back to [[Home]]. See also [[Guides/Plugins and sign-ins|Plugins and sign-ins]], [[Guides/Writing a routine|Writing a routine]].

A plugin is any MCP server: one that exists, or one you write. How to add one is in the app itself:

```sh
procauto guide plugins
```

It covers the three ways `procauto plugins add <name>` adds a server (`--url`, `--command`, `--dir`), a minimal plugin folder with its `plugin.yaml`, the permissions a server can declare, the annotation and secret rules, and the commands to check it. `plugins add --help` lists every flag. `procauto plugins remove <name>` takes a server out again and forgets its credentials. Names are lowercase letters, digits and dashes; `notify` is taken by the plugin the app ships with.

## Plugin folders
- The folder is copied into the app, so where it was written doesn't matter; the server runs from the copy, which is its working directory. `--replace` installs a new version (the old one stays if the new one fails its trial); `plugins remove` deletes the copy
- `plugins add` tries the server first: it must start, complete the MCP handshake and offer at least one tool, each with an object input schema and a name used once. Other servers' tools without annotations are only a warning, and count as writes
- A server that needs credentials is tried as far as it can be; sign in with `auth login <name>`, then `plugins check <name>` tries it fully
- `pairing` is for local apps that approve clients themselves and then show a credential once: `auth login` connects first so the app can ask, then asks you to paste the credential

## In the config file
Servers can also be written by hand under `mcpServers` in the config file (`procauto paths` shows where). Unknown or misspelled fields are an error, so a typo shows up immediately instead of being ignored. Secrets are `{secret-key}` references in `env` or `headers`, asked for by `procauto auth login <name>`; then `procauto plugins check <name>` tries the server.

### Local command (stdio)

```yaml
mcpServers:
  notes:
    command: /usr/local/bin/notes-mcp   # an absolute path, or a command on your PATH
    args: [--read-only]
    env:
      NOTES_TOKEN: "{notes-token}"   # asked for by `auth login notes`
      NOTES_MODE: fast               # plain values are fine too
    permissions: [full-disk-access]  # what macOS must allow it (`guide plugins` lists them)
```

A server's `permissions` (or `--permission` when adding it) are what routines using it need too: their review lists them, and they don't run while one isn't granted.

### Remote server

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
