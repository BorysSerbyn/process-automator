Back to [[Home]]. See also [[Guides/The menu bar app|The menu bar app]], [[Guides/Routines#Read what it did|Read what it did]].

## Start with `doctor`
```sh
procauto doctor
```

It explains a red dot without calling the model or any tool: whether the scheduler is responding, and for each enabled routine whether it loads, is trusted, its folder exists, its servers connect (signed in) and offer its tools, and its last run went well. Each problem comes with its fix, often the command to run.

Narrower checks:
- `procauto routines check [<name>]`: whether a routine would run (file, settings, servers, allowed tools), paused or not
- `procauto plugins check [<name>]`: whether servers start and what tools they offer
- `procauto auth status`: which servers are signed in
- `procauto usage`: where you stand against the usage limit

## Why the dot is red
The menu lists each reason; click one to open the run it's about.

| Reason | What to do |
|---|---|
| Not responding | The scheduler isn't running (after **Quit**, until you open the app or log in again). Open the app, or run `procauto login-items start`. `procauto login-items status` shows whether it starts at login; if it keeps stopping, `procauto daemon` in a terminal shows why |
| The scheduler can't work | The detail says why (on hover, or in `doctor`) |
| Setup isn't finished | Open **Finish Setup…**, or run `procauto setup` ([[Guides/Getting started#Set it up\|Set it up]]) |
| *routine* can't be scheduled | Usually not trusted (after a change to what it can reach), a missing folder, a server that isn't added, or a required parameter without a value. The detail says which; `routines trust <name>` for trust |
| *routine* needs a sign-in | `procauto auth login <server>` |
| *routine* or *hook* needs a permission | A plugin, program or watcher it uses needs something macOS hasn't allowed the app (Input Monitoring, Notifications, …). `procauto permissions request <permission>` asks macOS; if it asked before, turn it on at the System Settings address the command prints. `procauto permissions list` shows what needs what. "Not checked yet" means the scheduler hasn't run yet |
| *routine* failed | Read the run (`runs show <id>`, or click the reason). Fix it, then run it again |
| *routine* was skipped | Over the usage limit ([[Guides/Settings#Usage limit\|Usage limit]]) |
| *routine* missed its run | The computer was asleep or off past the routine's `staleness`, or other runs took too long. Run it by hand if it still matters (`run <name>`, or `run <name> --date <day>` for a past day) |
| *routine* didn't start at *time* | Its time passed without a run: the scheduler may be stuck. Check the scheduler log |
| *routine* is stuck | Its run is going past its timeout. `procauto stop <name>` |
| *hook* can't run | A hook that's on, whose watcher is missing, not trusted or failing (its polls fail, or its stream keeps ending), or which isn't trusted. `procauto hooks show <name>` and `procauto watchers show <watcher>` say which; a watcher that isn't trusted is trusted with the hook (`procauto hooks trust <name>`, or **Turn On…** in its window) ([[Guides/Writing a hook\|Writing a hook]]) |
| *hook* was skipped | It was paused for running far past its rate: its action may set it off again. Fix that, then `procauto hooks resume <name>` |

A routine stays red until its next run goes well (or you pause it).

## Run history
`procauto runs` lists recent runs with their outcome and reason; `procauto runs show <id>` shows one in full: its inputs, every tool call, what it wrote (and any write errors), tokens and its final reply. In the menu bar app, a routine window's **Runs** tab shows the same. Dry runs aren't recorded: run them in a terminal (`run <name> --dry-run`) to see what they do.

## Logs
| | |
|---|---|
| Scheduler (macOS) | `~/Library/Logs/process-automator/scheduler.log` |
| Scheduler (Linux) | `journalctl --user -u procauto-scheduler` |
| Tray and its windows (Linux) | `frontend.log` in the data folder (started again past 1 MB; the one before is `frontend.log.old`): why a window opened in a terminal, why the scheduler didn't start |
| Runs started in the background (`start`, **Run Now** in the menu bar app) | `start.log` in the data folder |
| Updates started from the menu | `update.log` in the data folder |
| Changes made by Claude through the MCP server | `mcp-changes.jsonl` in the data folder |

`procauto paths` shows the data folder (and `frontend log`).

## Common problems
- **A server's tool is missing from `routines check`**: the server doesn't offer it, often for lack of a scope or permission on your account. Check the server's scopes, sign in again, and look at `procauto mcp tools <server>`
- **"Claude Code isn't installed"**: the `claude-code` backend can't find `claude`. Install Claude Code and sign in to it; the app looks it up on your login shell's `PATH`
- **Claude Code isn't logged in during scheduled runs**: store a token with `claude setup-token` and `procauto auth login claude-code`
- **The model reloads between a run's turns** (`local` backend, Ollama): a tool call took longer than `llm.local.keep-alive` (1 minute), so Ollama freed the model: raise it (`settings set llm.local.keep-alive 5m`), or empty it to keep Ollama's own 5 minutes
- **"no model is set"** or **"no model to run it on"** (`local` backend): set the model routines run on, its name on your server: `procauto settings set llm.local.model qwen3:8b` (`ollama list` shows the ones you've pulled)
- **"needs the local server"** or **"it runs on local/…, but"** (`local`, or a `local/…` model on `claude-api`): `nothing answers at …` means the server isn't running at `llm.local.url` (for Ollama, start it); `doesn't have the model` means it has no model by that name (for Ollama, `ollama pull <model>`); `needs an API key` means `procauto auth login local`. A run that fails with "ended without reporting an outcome", or keeps calling tools wrong, is usually a model too small to call tools reliably: try a bigger one
- **"Codex isn't installed"** or **"Codex isn't signed in"**: the `codex` backend can't find `codex` on your login shell's `PATH`, or `codex login status` says it isn't signed in. Install the Codex CLI and run `codex login`. Runs read the sign-in from `~/.codex/auth.json`: one Codex keeps in the system keyring (`cli_auth_credentials_store`) isn't seen
- **"Codex's usage limit is reached"**: the run hit your Codex plan's limit; it says when to try again. The app can't see Codex's usage ahead of time, so scheduled runs still start
- **A secret "can't be read"** (`auth status` shows a problem, or a backend or routine says so): the Keychain (macOS) or keyring (Linux) holding it is locked. On macOS, unlock the login Keychain in Keychain Access. On Linux it says **"the keyring is locked"**: your secrets are in the Secret Service keyring, which is locked. A desktop session unlocks it when you log in; on a machine without one, unlock it yourself (for GNOME Keyring, `gnome-keyring-daemon --unlock`), then try again. Other `secret-tool` errors (no keyring running) are fixed the same way
- **`notify_me` fails: "no desktop notification service is running"** (Linux): routines' notifications need a desktop session's notification service; on a machine without a desktop, leave `notify` out of the routine
- **A local server isn't found by the scheduler**: give its command as an absolute path, or make sure it's on your login shell's `PATH`
