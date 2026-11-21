Back to [[Home]]. See also [[Guides/Adding a plugin|Adding a plugin]], [[Guides/Writing a routine|Writing a routine]].

Routines get their tools from plugins: MCP servers. A routine names the servers it uses in `automator.servers` ([[Guides/Writing a routine|Writing a routine]]); you sign in to the ones that need it once, and the app keeps the credentials in the Keychain (on Linux, the Secret Service keyring).

```sh
procauto plugins list                # every server routines can use
procauto auth status                 # which are signed in
procauto auth login <server>         # sign in
procauto auth logout <server>        # forget its credentials
procauto plugins check [<server>]    # start it and list what routines would get
procauto mcp tools <server>          # its tools (--json marks the read-only ones)
procauto mcp call <server> <tool> '<json args>'   # call one tool by hand (writes run for real)
```

`auth login` opens your browser for OAuth sign-ins and asks for keys and tokens in the terminal; with `--browser` it asks for them on a local page instead. `procauto setup` and the Setup window offer a sign-in for each server your routines use. Sign-ins are refreshed automatically; when one can't be, runs end with "needs a sign-in" and you run `auth login` again.

## Model backend
Routines run on one of two backends, set with `procauto settings set llm.backend <name>`:

- **`claude-code`**: Claude Code on your Claude plan. Install [Claude Code](https://claude.com/claude-code) and sign in to it (run `claude` once). If scheduled runs say Claude Code isn't logged in, run `claude setup-token` and store its token with `procauto auth login claude-code`
- **`api`** (the default): an Anthropic API key, paid per token. Store it with `procauto auth login anthropic`

Each has its own usage limit ([[Guides/Settings#Usage limit|Usage limit]]).

## The plugin that ships with the app
The app ships with no integrations; one plugin comes with it and needs no sign-in:

| Server | What routines get |
|---|---|
| `notify` | `notify_me`: a desktop notification to you ([[Guides/Writing a routine#Notifying you\|Notifying you]]) |

Everything else is a server you add: see [[Guides/Adding a plugin|Adding a plugin]].

## Signing in to what you add
What `auth login <server>` asks for depends on how the server was added:

| Server | Sign-in |
|---|---|
| Remote, OAuth (the default) | Opens the browser; tokens are kept and refreshed |
| Remote, OAuth without automatic client registration (`oauthClient`) | Asks for your own OAuth app's client ID and secret the first time, then opens the browser |
| Remote, API key in a header (`auth: headers`) | Asks for each `{secret}` its headers reference |
| Remote, public (`auth: none`) | Nothing |
| Local command | Asks for each `{secret}` its `env` references; a server with `pairing` connects first so it can show its own approval, then asks for the credential it gave you |

A server may only offer some tools once you've granted it the right scopes; if a routine's tool is missing, check the server's scopes and sign in again.
