Back to [[Home]]. See also [[Guides/The menu bar app#Settings|Settings in the app]].

```sh
procauto settings list                      # every setting: value, default, description
procauto settings list --scope app          # only the app-wide ones (--scope routines: each routine's)
procauto settings list --routine digest     # one routine's
procauto settings get llm.backend
procauto settings set llm.backend claude-code
procauto settings reset llm.backend         # back to the default
```

`settings set` checks the value before saving it, and changes apply at once (the scheduler re-reads them). In the menu bar app, **Settings** holds the app-wide ones and each routine's screen its own.

## App-wide
| Setting | Default | |
|---|---|---|
| `llm.backend` | `claude-api` | What runs your routines: `claude-code`, Claude Code on your Claude subscription (recommended); `claude-api`, Anthropic's API, paid per token; `local`, your own model server, e.g. Ollama (experimental, untested); `codex`, OpenAI's Codex CLI (experimental, untested). See [[Guides/Plugins and sign-ins#Model backend\|Model backend]] |
| `llm.local.url` | `http://localhost:11434/v1` | With `local` (and `claude-api`'s `local/…` routines): your model server. See [[Guides/Plugins and sign-ins#Your own model server (experimental, untested)\|Your own model server]] |
| `llm.local.model` | | With `local`: the model routines run on, its name on the server (e.g. `qwen3:8b`) |
| `llm.local.keep-alive` | `1m` | How long the local server keeps the model loaded after the last request from anyone (Ollama; other servers ignore it). `0` unloads after each request; empty leaves the server's own (Ollama: 5 minutes) |
| `llm.usage-limit` | `90%` | With `claude-code`: see [[#Usage limit]] |
| `llm.budget.window`, `llm.budget.window-usd`, `llm.budget.weekly-usd` | `5h`, `2`, `20` | With `claude-api`: see [[#Usage limit]] |
| `history.retention-days` | `90` | How long run history and logs are kept |
| `runs.failure-notifications` | `true` | The app's own notification when a run fails, needs a sign-in, is missed or skipped |
| `memories.max-chars` | `4000` | How much each memories file may hold |
| `update.auto` | `true` | Install new releases by themselves at a quiet moment (also the switch in **Updates**); see [[Guides/Getting started#Update\|Update]] |
| `update.source-dir` | | The checkout `update` builds from when no release has a download for this machine (empty: the one this build came from) |

## Per routine
Each routine has settings `routines.<name>.<field>`: `enabled`, `schedule`, `staleness`, `timeout`, `effort`, `folder`, `session`, `concurrency`, `builtinTools`, and `params.<param>` for each parameter. The routine's file gives the defaults; a setting overrides the file without editing it. `procauto guide routines` says what each one means ([[Guides/Writing a routine|Writing a routine]]).

```sh
procauto settings set routines.digest.timeout 30m
procauto settings set routines.digest.params.repo my-org/other-repo
procauto settings set routines.digest.builtinTools Read,Grep    # lists are comma-separated here (codex: e.g. shell,web_search)
```

## Usage limit
Keeps scheduled runs from using too much, depending on the backend:

- **`claude-code`**: `llm.usage-limit` is a share of your Claude plan's 5-hour and weekly usage. While either is above it, scheduled runs don't start. A run that hits the plan's own limit stops scheduled runs until the limit resets. The app reads your plan's usage from Claude Code itself, with nothing to set up. If it ever can't, setup asks to read it from your Claude Code status line instead (`procauto usage statusline install`): your status line keeps showing what it shows now, and `procauto usage statusline uninstall` puts it back as it was
- **`claude-api`**: a dollar budget. Scheduled runs don't start once API spend in the rolling window (`llm.budget.window`, e.g. `5h`) passes `llm.budget.window-usd`, or spend over 7 days passes `llm.budget.weekly-usd`. `0` means no limit. Runs on your own server have no price and don't count
- **`local`** and **`codex`**: none. The app can't read Codex's usage or price your server's runs, so neither setting applies; a run that hits Codex's own limit fails and says so

A run that doesn't start is recorded as skipped, with the reason, and the dot turns red. Runs you start by hand still run, with a warning. `procauto usage` shows where you stand; so does the **LLM usage** card in the Settings window.

## Start at login
The scheduler and the menu bar app (on Linux, the tray, in desktop sessions) start at login. `procauto login-items status|enable|disable` (or **Start at login** in Settings) changes that; it doesn't start or stop what's running now.
