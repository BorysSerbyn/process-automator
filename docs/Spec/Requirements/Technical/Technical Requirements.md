Back to [[Home]]. See also [[Spec/Requirements/Functional/Functional Requirements|Functional Requirements]], [[Spec/Requirements/Technical/Distribution|Technical: Distribution]]. Why it's built this way: [[Spec/Technical Decisions|Technical Decisions]].

## Core
- **Go core, one binary**: `procauto` (the Go module in `core/`, main package `core/cmd/procauto`); `core/internal/engine` is the engine (loading, readiness, runs, the scheduler; Genkit for the model), `core/internal/cli` the CLI over it
- **A small public surface; the rest is internal**: `core/automator` (`App`, `Main`, `CheckTemplates`) and `core/pluginapi` (the plugin contract). An app is an `automator.App` handed to `automator.Main` by its main package:

| Field | What it sets | Process Automator |
|---|---|---|
| `Name` | What people see: the app, its windows, its messages | `Process Automator` |
| `Command` | The CLI's name, and the commands messages suggest | `procauto` |
| `Folder` | The config and data folder's name, the logs folder, the release artifacts and the Linux install | `process-automator` |
| `BundlePrefix` | The prefix of the bundle ID (`<prefix>.<Command>`), which names the login items and the secrets service | `dev` |
| `ReleaseRepo`, `TeamID` | Where `update` gets releases, and the team that must sign them | `REPO`, `TEAM_ID` in `install.sh` |
| `Plugins` | In-process plugins compiled in | none |

- **Build values are set at link time** in `core/automator` by the Makefile: the version, the source checkout (`SOURCE_DIR`), where the templates are packaged, the release API, and `BUNDLE_PREFIX` over the app's prefix
- **Another app** is a Go module that requires this one and has its own main package, plugins and templates; it builds with this repository's Makefile and scripts at the same version (see [[Spec/Requirements/Technical/Distribution|Technical: Distribution]])
- **Blank-slate engine**: the engine knows no specific service and adds no plugin; routines use the plugins the app compiles in (its `automator.App.Plugins`; procauto's is `notify`, from `core/plugins`) and the MCP servers added in config. Nothing in `core/internal/` imports a plugin; the engine runs only the list it's given
- **OS-neutral core**: OS calls live in per-OS files (`name_darwin.go`, `name_unix.go`, `name_other.go`, or a `//go:build` line), OS-bound integrations are plugins, and delivering the app's own state to the OS (its notifications, the menu bar) is the frontends' job. The one exception is the notify plugin, which shows routines' notifications itself (see [[#Plugins in `core/plugins`]]). `internal/osboundary`'s test (no `runtime.GOOS`, `syscall`, OS tools or OS paths in shared files) and CI's Linux and Windows cross-compiles enforce it
- **Config**: one YAML file, `config.yaml` in the per-user config folder (`os.UserConfigDir()/<Folder>/`: `process-automator/`), decoded strictly: unknown or retired fields are errors. It holds `mcpServers` (external servers), `plugins` (in-process plugins' own sections) and `settings`. `--config` points the CLI at another file
- **Data folder** is the config file's folder; everything the app keeps is in it:

| Path | What |
|---|---|
| `routines/` | Routine files and partials |
| `run-status.json` | Run status (frontends follow it through `status --watch`) |
| `history/<routine>/` | One record per due run |
| `notifications.json` | The app's own notifications (frontends get them through `notifications list`) |
| `memories/` | `_global.md` and `<routine>.md` |
| `trust.json` | What each routine was trusted with |
| `plugins/<name>/` | Installed plugin folders |
| `plugin-data/<name>/` | In-process plugins' own data |
| `scheduler-state.json` | Handled slots, per routine and trigger |
| `plan-usage.json`, `statusline.json` | The Claude plan's last usage reading, and the status line the app wraps |
| `login-path` | The login shell's PATH, saved by the scheduler |
| `mcp-changes.jsonl` | Side-effecting MCP calls |
| `start.log`, `update.log` | Background runs and updates |
| `update-progress.json` | How the latest update went, or is going (`update progress`) |
| `reports/` | Readable run reports |
| `locks/`, `run.lock`, `edit.lock`, `daemon.lock`, `stop/` | Cross-process locks and stop requests |

- **Files are written atomically**, and shared state written during runs has one writer per file

## Frontends and the contract
- **The CLI is the API**: every command a frontend needs supports `--json` with a stable shape; frontends read none of the core's files (config, run status, notifications, history): only what the CLI prints. Run status comes from `status --json`, or `status --watch`, which prints it as one JSON line at once and again whenever it changes (it checks the file once a second) until stopped; the MCP server doesn't offer `--watch`, which never ends. automator-ui and Claude (through `mcp serve`) are frontends over it; none has its own logic or routines, and anything a frontend needs is added to the core for every frontend
- **Contract in `contracts/`**, generated from code by `make contracts` and committed; a test fails when it drifts:

| File | What |
|---|---|
| `cli.yaml` | Every command: usage, flags, the schema of its `--json` output |
| `cli/<command>.schema.json` | Each `--json` output |
| `settings.schema.json` | Every setting: type, default, limits, unit, description |
| `run-status.schema.json`, `notifications.schema.json` | The core's own files, as `status --json` and `notifications list --json` print them |
| `openapi.yaml` | Every type a frontend reads, as OpenAPI components; automator-ui generates its Swift types from it at build time |
| `examples/` | Hand-written examples both test suites check |

- **Errors as JSON**: a command given `--json` that fails prints `{"error": "..."}` on stdout; checks that report their own problems (`doctor`, `routines check`, `plugins check`) print only their result
- **Edits are serialized across processes**: config and settings writes, and each whole change to routines or plugins (`edit.lock`), so concurrent frontends and tool calls never lose each other's changes or interleave a check with another's write
- **Login-shell PATH**: the scheduler resolves the user's login-shell PATH at startup and saves it (`login-path`); every command adds the saved one to its own (resolving it once when there's none and no terminal), so a command a frontend starts finds `claude` and MCP server commands as one typed in a terminal does

## Settings
- **One registry**: every app-wide setting is declared once in `core/internal/settings/registry.go` (key, type, default, limits, unit, backend, description); `settings list/get/set/reset`, validation, `settings.schema.json` and the settings UI derive from it. Values live under `settings:` in the config file, edited in place with comments kept
- **Types**: bool, int, number, percent, duration, cron, enum, string, path (`~/` expands), list of strings (a YAML list; comma-separated on the CLI; items optionally limited to an enum)
- **Live**: the scheduler re-reads config and settings every pass, so changes apply without a restart. A command that changes something (not one run read-only) records each routine's state in run status itself (paused or not, next run, why it can't be scheduled), as a pass would, so frontends show it at once
- `settings list --scope app|routines`, `--routine <name>`; settings for the other backend show as `inactive`
- **Retired settings** (`settings.Retired`): a renamed or removed key in the config file reads as its new key (one already set wins) or not at all, and the next `settings set`/`reset` rewrites the file so. Retired: `notifications.failures` (now `runs.failure-notifications`), `notifications.on-click` (removed). A routine's `onNotificationClick` is gone too, from settings (ignored) and routine files (an unknown field)

| Setting | Default | |
|---|---|---|
| `llm.backend` | `api` | `api` or `claude-code` |
| `llm.usage-limit` | `90` (%) | `claude-code` only |
| `llm.budget.window` | `5h` | `api` only |
| `llm.budget.window-usd` | `2` | `api` only; 0 is no limit |
| `llm.budget.weekly-usd` | `20` | `api` only; 0 is no limit |
| `history.retention-days` | `90` | |
| `runs.failure-notifications` | `true` | The app's own notifications for runs |
| `memories.max-chars` | `4000` | |
| `update.source-dir` | empty | The checkout `update` builds from when no release has a download for this machine |

## Routines
- **One Dotprompt file per routine**: `<data>/routines/<name>.prompt`; the file name is the routine name (lowercase letters, digits and dashes). Parsed with Dotprompt's own parser
- **Dotprompt's directory conventions**: `_<name>.prompt` files are partials (`{{> name}}`); `memories` is reserved for the app
- **Frontmatter**: plain Dotprompt (`description`, `model`, `config`, `tools` = the allowlist, `output`) plus the app's fields as Dotprompt extension fields in the `automator` namespace, written dotted (`automator.servers:`). A nested `automator:` block, an unknown `automator.*` field, invalid YAML and an empty file are rejected; `tools` without `automator.servers`, and `automator.staleness` without `automator.schedule`, are errors
- **Each file loads independently**: a broken file is reported (`routines list`, warnings) and skipped, never taking down the others
- **Per-routine fields are declared once** (`settings.RoutineFields`: name, type, default, description, scheduled-only); parsing, validation, `settings`, the settings schema (`routines.<name>.<field>`) and `routines list --json` derive from it. Each is a frontmatter key `automator.<field>`, overridable by the setting `routines.<name>.<field>`; absent means the default

| Field | Default | Meaning |
|---|---|---|
| `enabled` | `true` | Runs on its schedule (setting only; created and installed routines start `false`) |
| `schedule` | none | Cron, local time; none means manual only. A routine without one in its file can't be given one through settings alone |
| `staleness` | `1h` | How late a missed slot may still run |
| `timeout` | `20m` | Counted from when the run starts (not while queued); the deadline is in `lastRun.deadline`; stuck at deadline + 5m |
| `effort` | `default` | `default`, `low`, `medium`, `high`, `xhigh`, `max`; the agent CLI's `--effort`; the `api` backend takes only `default` |
| `folder` | an empty temporary folder (with `session: save`, `<data>/session-folders/<name>/`, kept) | Absolute or `~/`; must exist. Its `CLAUDE.md` is added to the system prompt by the core; its settings and hooks are never loaded |
| `session` | `discard` | `save` keeps the agent CLI session: the core picks its ID (`--session-id`) and records it in `lastRun.sessionId` and history, with the folder the run worked in (`folder`) that it resumes from. Independent of `folder`: without one, runs work in the routine's own kept folder; dry runs never keep a session |
| `builtinTools` | `[]` | Agent CLI built-in tools by name, allowed explicitly, never by permission bypass; file tools stay inside the working folder. Agent CLI backends only |
| `concurrency` | `queue` | `queue` or `parallel` |

- **Servers and tools**: `automator.servers` lists the servers the routine uses; every tool in `tools` must be offered by exactly one of them, and only those tools are registered with the model
- **Parameters** (`automator.parameters`: name, setting type, default, description, enum): values are per-routine settings `routines.<name>.params.<param>`, passed to the prompt as inputs; a name is lowercase letters, digits and underscores and can't shadow a built-in input; a required one without a value makes the routine unschedulable
- **Platforms** (`automator.platforms: [macos, linux]`): empty means all
- **Backends** (`automator.backends: [claude-code, api]`, `llm.backend`'s values): the backends it works with; empty means any
- **Plugin sections** (`automator.plugins.<name>`, dotted or nested) may only name in-process plugins in the routine's servers that declare they take one
- **Inputs** every routine gets: `today`, `now`, `since` (the start of the previous workday) and `trigger`, plus its parameters
- **Commands**: `routines list/show/create/update/delete/check/pause/resume/trust`. Create never overwrites; update replaces only an existing file; both validate like loading, from `--from <file|->` or `--source`. A created routine's disabled setting is written before its file, so the scheduler never sees it enabled
- **`routines list --json`** gives each routine's effective settings, its folder, its schedule in words (`scheduleText`), and its model and effort as the configured backend's agent CLI takes them (`agentModel`, `agentEffort`)
- **`routines check [<name>]`** parses each file, resolves its settings, checks its folder exists and every server it uses (directly or through its plugins) is provided, connects its servers and confirms each allowlisted tool is offered by exactly one of them, and checks the servers are signed in, without calling the model
- **Schedules in words** come from the core: `schedule describe <cron>` gives the words, the next few times, and the schedule as days of the week and a time when it is one (`picker`); `schedule build --days … --at HH:MM` turns those back into cron

## Templates
- **Files shipped with the app**, never compiled in: `Contents/Resources/templates` in the `.app`, `share/<Folder>/templates` in the Linux tree, the checkout's `templates/` in development builds; `paths` shows the folder. An empty or missing folder lists none; the repository's `templates/` holds only a README
- **Checked like the app's own**: every template loads as a routine, includes `{{> memories}}`, and renders with the standard inputs, anchored in `today` or `since`, with no template syntax left (`automator.CheckTemplates`, run by the tests on the shipped folder)
- **Tags are folders**, general to specific; `projects/<project>/…` makes a template that project's (`project` in `templates list`). Installing copies a template flat into the routines folder
- `templates list [--all]` shows the ones for this OS (tags, project, parameters, platforms, backends, whether it supports the selected backend (`supportsBackend`), installed or not); `templates install <tags>/<name> [--as <name>] [--set param=value]` refuses a template that doesn't support the selected `llm.backend` (saying how to switch), validates every value first, never overwrites a routine, stores values as `routines.<name>.params.<param>`, prints the template's description, and leaves the routine disabled and untrusted

## Model backends
- **`llm.backend`** picks the loop behind one `Backend` interface:
  - `api`: Genkit runs the loop against the Anthropic API with the key stored by `auth login anthropic`
  - `claude-code`: headless Claude Code (`claude -p`) on the user's Claude plan, signed in through Claude Code itself or a `claude setup-token` token stored by `auth login claude-code`
- **Agent CLI backends** are one generic `CLI` backend plus a small `CLITool` per CLI (how to start it headless and isolated, its MCP config, environment and model naming, and how to read its result); a test keeps them in step with the CLI's backend list and the `llm.backend` values
- **Shared by both**: Dotprompt rendering through Genkit (inputs, partials, memories) and the tool gateway (allowlist, call log, dry-run interception). For agent CLIs, the gateway's tools are served over a loopback MCP server guarded by a random bearer token, named `procauto`
- **Claude Code is isolated** from the user's own setup: `--tools` limited to the routine's built-in tools (none by default), `--strict-mcp-config`, `--permission-mode dontAsk`, `--setting-sources ""`, `--restricted`, `--disable-slash-commands`, `--no-chrome`, a replaced system prompt, a capped number of turns, and `--no-session-persistence` unless the session is saved. `ANTHROPIC_API_KEY` and other inherited credentials are stripped; `CLAUDE_CODE_DISABLE_AUTO_MEMORY` and `CLAUDE_CODE_DISABLE_ORG_MEMORY` are set
- **With `claude-code`** only `model` from the frontmatter applies; `config` is ignored, and only Claude models run
- **Keep awake**: every run keeps the computer from idle-sleeping until it finishes (`caffeinate` on macOS, tied to the process)

## Runs
- **Occurrences**: every run carries what triggered it: kind (`schedule`, `manual`, `backfill`), a unique ID, the time it's for, optional data. Routines get it as the `trigger` input, and `today`/`since`/`now` are computed from its time (a scheduled run's `now` is its slot). Run status (`lastRun.trigger`, `lastRun.for`) and history (`trigger`, `triggerId`, `for`) record it
- **Backfill**: `run <routine> --date YYYY-MM-DD` runs as of that day (today or earlier): at the routine's first scheduled time that day, or the current time of day if it has none. Works with `--dry-run`, recorded with trigger `backfill`, never touches scheduler state
- **`run`** runs in the terminal and prints the reply; **`start`** runs detached and returns at once, logging to `start.log`, followed through run status and history
- **Outcome**, strongest first:
  1. a backend error (model or CLI error, max turns, timeout): `failed`
  2. a sign-in failing mid-run: `needs-reauth`
  3. a write tool whose last attempt failed: `failed`, whatever the model says (a retry that succeeds is fine)
  4. the routine's own report through the built-in `report_outcome` tool (`done`, `nothing_to_do`, `failed` + reason), which both backends tell the model to call. `failed`, or no report at all, fails the run; `nothing_to_do` is a success with its reason
- **Tool errors go back to the model** inside the loop; rules 2 and 3 turn an unrecovered one into a failed run. A source that can't be read is an error, never an empty result
- **Structured results**: a routine may declare a Dotprompt `output:` schema; the agent requests that shape (Genkit output schema; `claude -p --json-schema`), validates it (a missing or invalid result fails the run) and stores it in history as the run's `output`, next to the text. A valid result without a reported outcome counts as done; a reported outcome still decides
- **History**: every due run leaves a record in `history/<routine>/` (executed, couldn't start, missed, interrupted, stopped) with outcome, reason, inputs, tool calls and writes with their errors, tokens (and cost with `api`) and the final text. Dry runs leave none. `runs [<routine>] [--since 7d|<date>] [--limit N]` lists them; `runs show <id> [--open]` shows one in full, or as a readable report. Pruned by `history.retention-days`
- **Queue and concurrency**:
  - `queue` routines, scheduled or run by hand, take a device-wide cross-process queue lock: one at a time, tightest staleness first, staleness re-checked before each. `parallel` routines never take it
  - A run waiting for its turn (the queue, or its own routine's previous run) is `queued` (with `queuedAt`); only `running` counts for `busy`, the timeout and the stuck limit. Waiting is polled, so it can be stopped. A routine never overlaps itself: a slot due while it's still running waits (and is missed past its staleness)
  - OAuth refresh takes a per-credential cross-process lock around refresh-and-save, re-reading the token first and refreshing only if it's still stale
- **Stopping**: `stop <routine>` stops its run, running or queued, in whichever process runs it (a stop request the run picks up within a second), waits for it to end, and records it as `stopped`
- **Dry runs** (`--dry-run`) intercept every tool not declared read-only by its server (MCP `readOnlyHint`), returning what the write would have sent, and get only the read-only built-in tools (Read, Glob, Grep, WebSearch, WebFetch)
- **`mcp call <server> <tool> [json-args]`** calls one tool outside any routine, for debugging (writes run for real); `mcp tools <server>` lists a server's tools

## Scheduler
- **`procauto daemon`** runs the scheduler, started at login and kept alive by the OS (a LaunchAgent with `KeepAlive`, or a systemd user unit); the OS does no scheduling
- **Every 30s** it re-reads config, settings and routine files and runs what's due; it heartbeats run status every 10s (`validUntil` = now + 60s)
- **Slots**: the most recent unhandled slot of each routine is due; older than its staleness, it's recorded as `missed`, not run; only the latest slot counts after a long outage. Slots are marked handled before running (at most once). On first start only a slot within its staleness runs, and nothing is reported missed
- **Scheduler state** is kept per routine and per trigger (`scheduler-state.json`: a time for cron slots)
- **Queued routines** go to an in-process worker and parallel ones start on their own, so the scheduler never waits for runs. Shutdown cancels and records in-flight runs; runs found still `running` or `queued` at start were cut off and are failed as interrupted
- **Nothing only logged**: a routine it can't load at its slot is a failed run, a missed slot a missed run, and a scheduler that can't work at all (e.g. the config doesn't load) sets a `problem` in run status until it works again
- **Logs**: macOS `~/Library/Logs/process-automator/scheduler.log`; Linux the journal

## Run status and health
- **`run-status.json`** is the single record frontends read: `health` (`ok`, `error`, `none`), `busy`, `summary`, `reasons`, `setupLeft`, `problem`, `validUntil`, and per routine `enabled`, `problem`, `nextRunAt`, `lastRun` (trigger, for, ID, session ID, folder, started, finished, deadline, queued, outcome, reason), its own `health` and `summary`. Schema in `contracts/run-status.schema.json`
- **The core decides health**: a routine is unhealthy when its last run failed, needed a sign-in, was missed or skipped, when it can't be scheduled, when a slot wasn't picked up within 2 minutes, or when a run is past its timeout + 5 minutes (25 minutes without a deadline). Unfinished setup and a scheduler problem also make health `error`. `stopped` and `queued` don't
- **Reasons**: a short headline per problem with a `detail` and the run it's about, computed by the same rules as `health`
- **Busy** is set while a routine is `running`

## Plugins
- **External plugins** are `mcpServers.<name>` entries: a local `command` (`args`, `env`, `dir`) or a remote `url` (`transport`: `streamable` or `sse`; `auth`: `oauth` (default), `headers` or `none`; `headers`; `scopes`; `oauthClient` with `clientIdKey`, `clientSecretKey`, `redirectPort` for servers without dynamic client registration), plus `description`. Local servers that issue their own credential on first contact set `pairing`
- **Secrets are only referenced**: `{secret-key}` in `env` or `headers`; `auth login/logout/status` derive what to ask for from the spec, so a server needs no code
- **The plugin convention**: MCP plus every tool declaring annotations with `readOnlyHint`; a tool not marked read-only is a write
- **`plugins add <name>`** (`--url` or `--command` with `--arg`/`--env`, `--auth`, `--transport`, `--header`, `--scope`, `--description`, or `--dir`; `--replace`) writes the server to `mcpServers`, validated like config loading. Plugin names are lowercase letters, digits and dashes; local commands are absolute paths or names on PATH. `plugins remove` deletes it from config, then its installed folder and its credentials, keeping secrets another server still references
- **Trial**: `plugins add` starts the server, does the handshake and lists its tools before writing anything. Rejected if it doesn't answer within 90s, offers no tools, lists a tool twice, or has a non-object input schema; plugin folders also need annotations on every tool (a warning for other servers). It reports each tool as read-only or write. A server needing credentials is tried as far as possible (a remote one must at least answer HTTP); `plugins check [<name>]` runs the full trial after sign-in. The trial runs the server unsandboxed, under the approval `plugins add` already needs
- **Plugin folders**: `plugins add <name> --dir <folder>` copies a folder with a `plugin.yaml` (`command`, `args`, `env`, `description`) to `<data>/plugins/<name>/`, trying the staged copy before moving it into place; relative commands resolve inside it, and neither the command nor any symlink in the folder may point outside it. `--replace` keeps the old copy if the new one fails
- **In-process plugins** (`core/pluginapi`, public, a leaf package with no engine dependencies): a `Plugin` is a name, a description and either an `External` server spec or `Serve`; it may declare a `RoutineSection`, the servers it `Uses`, and `Prepare` (steps done outside the app before its first sign-in). Its `Host` gives `Connect` (another server), `ConnectApp` (the app's own MCP server, read-only: `<this executable> --config <config> mcp serve --read-only` over stdio), `AppName()` (the app's name, e.g. a notification's sender), `Config` (its `plugins.<name>` section, strict), `Secret` (only those it declares), `DataDir()` (`<data>/plugin-data/<name>/`), `RoutineConfig(v)` (the running routine's section, strict) and `Run()` (the routine, its run's history ID and folder). A host is made per session; outside a run, the routine parts are empty. A config `mcpServers.<name>` that clashes with an in-process plugin's name is an error
- **`plugins list`** shows every server routines can use; `mcp tools <server> [--json]` its tools, marking read-only ones

## Plugins in `core/plugins`
- **`notify`** (`core/plugins/notify`, built only on `pluginapi`, in an app only if its `Plugins` list it; procauto's do). It shares no code, type, file or setting with the app's own notifications: `notify_me(title, body?, urgent?)`, a write tool (dry runs intercept it) that shows a desktop notification itself, through the OS, not through the app's notifications file or automator-ui. It has no settings, and clicking one does nothing. The call is in the run's history like any tool call; nothing else is kept
  - **macOS**: UserNotifications, from a darwin-only cgo file (`deliver_darwin.go`, built `darwin && cgo`; the core is built with `MACOSX_DEPLOYMENT_TARGET=14.0`). The core runs from inside the app's bundle and is signed with the app's bundle ID as its identifier, so its notifications are the app's and share its permission; if macOS hasn't been asked, the first one asks. Outside an app bundle (a development build) it reports that it can't notify rather than calling UserNotifications. `urgent` is time-sensitive
  - **Linux**: the freedesktop notification service (`org.freedesktop.Notifications.Notify`) on the session bus, in pure Go (godbus), as the app's name; `urgent` is critical urgency. Linux builds stay CGO-free
  - **Other OSes** (and macOS without cgo): `notify_me` fails, saying it isn't supported
  - **Tests** replace the OS call (`notify.Deliver`) and never show a notification

## Sign-ins and secrets
- **`auth login <server|anthropic|claude-code>`**: OAuth in the browser for remote servers (dynamic client registration or the server's `oauthClient`, PKCE, rotating refresh tokens persisted); keys and tokens typed in the terminal, or on a short-lived local page with `--browser`. A plugin with `Prepare` steps shows them and waits for me to say they're done before asking for anything. Web addresses and Markdown links (`[text](https://…)`) in the steps are links on the secret page and in the Setup window; the terminal shows a Markdown link as its text and address. `auth logout` forgets a server's credentials; `auth status` shows which are stored
- **Unattended refresh**: a run never opens a browser; an expired or revoked credential is a distinct `needs-reauth` outcome naming the server
- **Secrets store** behind `secrets.Store`: the macOS Keychain through `/usr/bin/security`, and the Secret Service (GNOME Keyring, KWallet) through `secret-tool` on Linux; one item per key under the build's service (`dev.procauto` from the Makefile's default prefix, `process-automator` for a plain `go build`), values written on stdin (never argv) and base64-encoded; values stored by hand are read as-is. Without `secret-tool`, nothing is found and storing says what to install
- **Never in config**: secrets live only in the store

## Notifications
- **The app's own**, recorded by the core in `notifications.json` next to run status (schema in `contracts/notifications.schema.json`), by `kind`: runs that failed, needed a sign-in, were missed or skipped (`failure`, unless `runs.failure-notifications` is off) and update results (`app`). The newest 200 within 30 days are kept. Routines' notifications never come here (the notify plugin shows them itself); entries of the retired kind `routine` are dropped when the file is read
- **Delivered by frontends**, which mark them delivered (`notifications delivered <id>...`); `notifications list [--pending]` lists them. automator-ui delivers only while macOS allows notifications, so none are lost before the permission prompt is answered
- **Clicking opens the app**: the frontend runs `notifications open <id>`, which says what to show and opens nothing: `show-run` (its `routine` and `run`) for one about a run, `show-app` for one about the app itself (an update). The frontend shows it in its own windows
- **Opening** files and URLs uses `open` on macOS and `xdg-open` on Linux

## Usage limit
- **Checked when a scheduled run gets its turn**: over the limit, it's recorded as `skipped` with the reason (and a failure notification); manual runs and backfills go ahead, warning in their log. `usage [--json]` reports the backend in use (`kind`: `plan` or `budget`); `doctor` reports it when over
- **`claude-code`**: `llm.usage-limit`, a share of the Claude plan's 5-hour and all-models weekly usage (single models' weekly limits are shown, not applied)
  - Read from Claude Code: `claude -p /usage` with stream-json output carries a `usage_report` (`rate_limits.limits`, rows by `kind`: `session`, `weekly_all`, `weekly_scoped`), read defensively, using no tokens
  - Kept in `plan-usage.json` with when it was read; read again when over 10 minutes old (when the scheduler starts, before a scheduled run, when `usage` is asked), never more often, even when it fails. A failed read keeps the last reading; with none, runs go ahead. A window past its reset is back at 0%
  - **Status line fallback**: `usage statusline install` sets Claude Code's `statusLine` (`~/.claude/settings.json`, nothing else changed) to run `procauto usage statusline`, which records the status line's `rate_limits` and runs the status line I had with the same input (saved in `statusline.json`); `usage statusline uninstall` and uninstalling the app put it back. Setup's `usage` step asks for it while `/usage` can't be read: it answered without a usage report the app can read, or 3 tries in a row failed (one failed try isn't enough; a reading clears it); an unreadable `/usage` isn't a health problem
  - A run that hits the plan's limit records it until the reset (an hour when it gives no time). No cost is recorded or worked out for these runs
- **`api`**: budgets in USD per rolling window (`llm.budget.window`, `llm.budget.window-usd`) and per 7 days (`llm.budget.weekly-usd`), 0 meaning none: the sum of `api` runs' `costUsd`, priced by the core from their tokens with a price table (cache reads, cache writes at 1.25× input, output); a model missing from it isn't counted, and its run's log says so

## The app's MCP server
- **`procauto mcp serve [--read-only]`** (stdio) serves the CLI as tools to Claude and other MCP clients. It's the app's interface, not a plugin: routines don't get it, and plugins that need the app's routines or runs reach it read-only through `Host.ConnectApp`
- **`procauto mcp connect`** registers it with Claude Code at user scope: `claude mcp add --scope user procauto -- <procauto> mcp serve`, by the `~/.local/bin` link when there is one (else `procauto` on `PATH`, else the running binary), so updates don't break it. It's connected when Claude Code's config (`.claude.json` in `$CLAUDE_CONFIG_DIR`, else in my home folder) has a user-scope `mcpServers.procauto` whose command exists; read from the file, no `claude` process. Nothing changes when it's connected; one whose command is gone is replaced
- **Parity**: every CLI command is a tool, derived from the command tree at startup (arguments and flags as the input schema); each call runs the CLI as a subprocess (`--json` when available), its log lines streaming as progress. Arguments are checked against the tool's schema. Flags that read local files (`--from`) aren't exposed; a routine's source reaches the command on stdin
- **Cancelling** a call, or the server stopping, interrupts the command, which records its outcome
- **Safety on the command** (cobra annotations): `procauto/read-only`; `procauto/read-only-with: <flag>`, which adds a read-only variant tool (`run --dry-run`, `update --check`, `setup --json`); `procauto/not-read-only`. Unmarked commands are destructive, so clients ask before calling them
- **Overrides**, the one place a tool differs from its command (a test checks each names a real command): `daemon`, `mcp serve`, `mcp connect`, `help`, `completion`, `routines trust` and `uninstall` are excluded; `auth login` asks through the client; `update` reminds to reconnect
- **`--read-only` serves less**: only read-only tools, and nothing marked `not-read-only` (anything that runs, stops or sets up something, read-only variants included)
- **Secrets never pass through the model**: `auth login` shows the OAuth page, or the local page that stores a key straight in the secrets store, through URL elicitation, run across calls; if the client can't show URLs, the browser opens directly. A secret is never a tool argument
- **Change log**: every side-effecting call is recorded in `mcp-changes.jsonl`, with long values shortened, plugin env and header values left out, and one older file kept past 5 MB

## Memories
- **Plain Markdown files**, one per scope: `memories/_global.md` (every routine) and `memories/<routine>.md`, one memory per `- ` line; other lines are kept. Editable by hand
- `memory list/add/rm/edit` (`--routine <name>` for a routine's own; IDs come from the text). A run gets the global memories and its routine's through the `{{> memories}}` partial
- Each file is capped by `memories.max-chars`; past it, `memory add` asks to compact first. `memory compact [--routine <name>]` asks the model (structured output) to merge duplicates and drop replaced memories without adding anything, shows the change and saves only once confirmed (`--yes` without a terminal)
- Deleting a routine forgets its memories

## Guardrails
- **Allowlist** enforced by the shared agent code: only a routine's `tools` are registered with the model
- **Trust**: a routine runs for real only while what it can reach is what I trusted: its servers, allowlisted tools, built-in tools, plugin sections and folder (`trust.json`). Any change to those takes the trust away; its prompt, schedule, model and other settings don't
  - `routines trust <name> [--check] [--yes] [--resume]` shows what it can reach (tools split into writes and reads, by connecting to its servers) and what changed since it was last trusted, and asks; without a terminal it needs `--yes`; `--check` trusts nothing. `--resume` also resumes a paused routine that has a schedule, in the same yes (a trusted one is only asked about its schedule). Not an MCP tool
  - An untrusted enabled routine isn't scheduled (its problem says what changed and how to trust it); a real `run` of it is refused; dry runs never need trust
  - Created and installed routines start untrusted; deleting one forgets its trust. The first time trust is used, the routines already there are trusted as they are
- **Readiness**: an enabled routine whose folder is missing, that uses a server nothing provides, or lacks a required parameter isn't scheduled either; its problem says why. `doctor`, `routines check`, `setup` and the scheduler judge a routine with the same checks
- **`doctor [--json]`** explains a red indicator without calling the model or any tool: the scheduler responds (run status fresh, no scheduler problem); each enabled routine loads, its settings resolve, its folder exists, it's trusted, its servers connect (signed in) and offer its tools; its last run didn't fail, need a sign-in or get missed; usage is under its limit. Each finding has a fix; it fails if there are any

## automator-ui
automator-ui (SwiftUI, macOS 14+; `automator-ui/`, executable `AutomatorUI`, library `AutomatorUICore`) is the app: the indicator (menu bar icon with its status dot and menu, `Indicator/`), and the Settings, Setup and Routines windows. It shows what the core says and runs what the core offers; the one thing it judges is whether the core is responding.
- **Every action is a CLI command** with `--json`, run off the main thread
- **Follows run status** through `status --watch` (started again 5s after it ends); none yet, unreadable, or past `validUntil` (checked every 15s, so a stream that stopped goes red too) shows red, "Not responding". It reads no file and knows no path
- **Logo**: its icon and the indicator's glyph come from one source (`automator-ui/Resources/Logo/`): an hourglass drawn as one stroke whose head runs into the status dot
- **Menu**: one fixed width; text lines wrap or end in an ellipsis to fit (the full text on hover). The version; the status in the core's words (`reasons`, each opening its run with `runs show <id> --open`, else `summary`); Finish Setup… while `setupLeft` isn't empty; **Routines ›** (All Routines…, then each routine with its `health` and `summary`, opening its window); the update item (Updating to X… while one installs, opening the Updating window); Settings; Quit
- **Settings window**: Start at login, the LLM usage card (`usage --json`), then the app-wide settings (`settings list --scope app`) as a collapsible tree from their dotted keys, one control per setting type (toggle, picker, text field, file chooser), shown and sent as text (`valueText`, `defaultText`); a text field saves on Return and when it loses focus; the core's message shows when it refuses a value; Reset; settings declared ahead of their feature are tagged and disabled. It re-reads everything each time it opens
- **Setup window**: `setup --json`'s steps grouped by kind, each with its button, and Check Again. A routine's turn-on step shows its review (`routines trust <name> --check --json`) and one **Turn On** (`routines trust <name> --yes --resume`); an `auto` step runs once as soon as the window reads it, its button left for trying again
- **Routines window**: each routine with its health, schedule in words, last run and summary, leading to its window
- **A routine's window**: Run Now / Dry Run (`start <routine> [--dry-run]`), Stop while it runs or waits (`stop`), Pause / Resume (`routines pause|resume`), Delete… (`routines delete`, confirmed); a Not trusted banner with Review… (`routines trust <name> --check --json`, then `--yes`); its status, schedule with a days-and-time picker or custom cron (`schedule describe`/`build`, applied with `settings set` on Save), and what it can reach; its runs (`runs show`); its memories; its own settings (`settings list --routine <name>`)
- **Updates**: `update --check` on launch and daily; "Update to X…" confirms with the core's `note`, then installs with `update --detach`, which runs in the background so it survives automator-ui restarting, logs to `update.log`, and reports its result as a notification. An install holds `locks/update.lock`, so a second one refuses and the menu shows one in progress
- **Update progress**: while one installs (started here, or reported by `update --check`), `update progress --json` every 2 seconds until its `state` isn't `installing`. The Updating window (opened on starting one, and from the menu's Updating item) shows its release, `step` and `detail` with an indeterminate progress bar, then the error if it failed; closing it leaves the update running
- **Start at login**: `login-items status|enable|disable` over the build's login items (`ui` and `scheduler` on macOS, `scheduler` on Linux); launchd's enable/disable override on the installed LaunchAgents, or enabling the systemd user unit. It never starts or stops what's running; `installed` is false before the app is installed
- **Notifications** (the app's own): `notifications list --pending --json` when run status changes and every minute; each is shown, then marked delivered (`notifications delivered`); a click runs `notifications open <id> --json` and opens the app there: `show-run`, the routine's window on its Runs tab with that run open and in view; `show-app`, the Routines window
- Each window and its data are freed when it closes

## Linux
- The Go core runs unchanged; per-OS pieces have Linux versions behind the same interfaces: secrets through `secret-tool`, opening with `xdg-open`, start at login through the scheduler's systemd user unit
- No automator-ui: the CLI and the scheduler only
- Only templates for Linux (no `platforms`, or including `linux`) are offered
