Back to [[Home]]. See also [[Guides/The menu bar app#Settings window|Settings window]].

```sh
procauto settings list                      # every setting: value, default, description
procauto settings list --scope app          # only the app-wide ones (--scope routines: each routine's)
procauto settings list --routine digest     # one routine's
procauto settings get llm.backend
procauto settings set llm.backend claude-code
procauto settings reset llm.backend         # back to the default
```

`settings set` checks the value before saving it, and changes apply at once (the scheduler re-reads them). In the menu bar app, **Settings** holds the app-wide ones and each routine's window its own.

## App-wide
| Setting | Default | |
|---|---|---|
| `llm.backend` | `api` | What runs routines: `claude-code` (your Claude plan) or `api` (an Anthropic API key). See [[Guides/Plugins and sign-ins#Model backend\|Model backend]] |
| `llm.usage-limit` | `90%` | With `claude-code`: see [[#Usage limit]] |
| `llm.budget.window`, `llm.budget.window-usd`, `llm.budget.weekly-usd` | `5h`, `2`, `20` | With `api`: see [[#Usage limit]] |
| `history.retention-days` | `90` | How long run history and logs are kept |
| `runs.failure-notifications` | `true` | The app's own notification when a run fails, needs a sign-in, is missed or skipped |
| `memories.max-chars` | `4000` | How much each memories file may hold |
| `update.source-dir` | | The checkout `update` builds from when no release has a download for this machine (empty: the one this build came from) |

## Per routine
Each routine has settings `routines.<name>.<field>`: `enabled`, `schedule`, `staleness`, `timeout`, `effort`, `folder`, `session`, `concurrency`, `builtinTools`, and `params.<param>` for each parameter. The routine's file gives the defaults; a setting overrides the file without editing it. What each one means is in [[Guides/Writing a routine#Frontmatter|Writing a routine]].

```sh
procauto settings set routines.digest.timeout 30m
procauto settings set routines.digest.params.repo my-org/other-repo
procauto settings set routines.digest.builtinTools Read,Grep    # lists are comma-separated here
```

## Usage limit
Keeps scheduled runs from using too much, depending on the backend:

- **`claude-code`**: `llm.usage-limit` is a share of your Claude plan's 5-hour and weekly usage. While either is above it, scheduled runs don't start. A run that hits the plan's own limit stops scheduled runs until the limit resets. The app reads your plan's usage from Claude Code itself, with nothing to set up. If it ever can't, setup asks to read it from your Claude Code status line instead (`procauto usage statusline install`): your status line keeps showing what it shows now, and `procauto usage statusline uninstall` puts it back as it was
- **`api`**: a dollar budget. Scheduled runs don't start once API spend in the rolling window (`llm.budget.window`, e.g. `5h`) passes `llm.budget.window-usd`, or spend over 7 days passes `llm.budget.weekly-usd`. `0` means no limit

A run that doesn't start is recorded as skipped, with the reason, and the dot turns red. Runs you start by hand still run, with a warning. `procauto usage` shows where you stand; so does the **LLM usage** card in the Settings window.

## Start at login
The scheduler and the menu bar app start at login. `procauto login-items status|enable|disable` (or **Start at login** in the Settings window) changes that; it doesn't start or stop what's running now.
