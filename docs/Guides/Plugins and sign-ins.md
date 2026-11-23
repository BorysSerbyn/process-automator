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

## Two accounts of one service
In two Slack workspaces (or with two Google or Linear accounts), add the second as an account of the first, then sign in to it with its own credentials:

```sh
procauto plugins add slack-work --account-of slack
procauto auth login slack-work       # its own sign-in (its own app's client ID and secret, when the service needs one)
```

A routine can use both (`automator.servers: [slack, slack-work]`); a tool both offer is listed with its server, `slack/slack_search` and `slack-work/slack_search` (`procauto guide routines`). `plugins remove slack-work` forgets only that account's credentials.

## Model backend
Routines run on one of four backends, set with `procauto settings set llm.backend <name>` (or chosen in `procauto setup`):

- **`claude-code`** (recommended): Claude Code on your Claude plan. Install [Claude Code](https://claude.com/claude-code) and sign in to it (run `claude` once). If scheduled runs say Claude Code isn't logged in, run `claude setup-token` and store its token with `procauto auth login claude-code`
- **`claude-api`** (the default): Anthropic's API with your API key, paid per token. Store it with `procauto auth login anthropic`
- **`local`** (experimental, untested): your own model server (below)
- **`codex`** (experimental, untested: it has run only against a stand-in for OpenAI's API, never OpenAI itself; prefer `claude-code` or `claude-api`): OpenAI's [Codex CLI](https://developers.openai.com/codex/cli) on your Codex sign-in (a ChatGPT plan or an OpenAI API key). Install it and run `codex login` once; the app uses that sign-in and keeps none of its own. Runs don't load your Codex `config.toml`, and read the sign-in from `~/.codex/auth.json` (Codex's default), not from the system keyring

Claude Code and the API each have their own usage limit ([[Guides/Settings#Usage limit|Usage limit]]).

### Your own model server (experimental, untested)
On `local`, routines run on an OpenAI-compatible server you run or pay for: a local one (Ollama, llama.cpp's `llama-server`, vLLM, LM Studio) or a hosted one (DeepSeek, Qwen on DashScope, Kimi on Moonshot, ...). It has been tried only against a stand-in for the API and small models on Ollama, never with real work: prefer `claude-code` or `claude-api`.
- **The server**: `llm.local.url`, by default Ollama on this machine (`http://localhost:11434/v1`). Install Ollama and pull a model yourself first (`ollama pull qwen3:8b`)
- **The model**: `llm.local.model`, its name on the server (`qwen3:8b` on Ollama, `deepseek-chat` on DeepSeek). Every routine runs on it, whatever model its file names; a routine that names `local/<model>` runs on that one instead. Pick one that calls tools: routines need it to, and small models often don't, or call them wrong
- **Its API key**, for a server that needs one (hosted ones do; Ollama doesn't): `procauto auth login local`
- `procauto setup` asks for the server and lists its models to pick from; `procauto doctor` asks the server whether it answers and has the model. Its runs have no list price and no usage limit
- **Mixing with Claude**: on `claude-api`, a routine whose model is `local/<model>` runs on this server too (with the same URL and key), and when every routine does, no Anthropic key is needed
- **Former names** still work: `openai-compat/<model>` models, the `llm.openai-compat.base-url` setting and a key stored with `auth login openai-compat`

## The plugin that ships with the app
The app ships with no integrations; one plugin comes with it and needs no sign-in:

| Server | What routines get |
|---|---|
| `notify` | `notify_me`: a desktop notification to you, shown by the plugin itself (macOS, Linux) ([[Guides/Writing a routine\|Writing a routine]]) |

Everything else is a server you add: see `procauto guide plugins` and [[Guides/Adding a plugin|Adding a plugin]].

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
