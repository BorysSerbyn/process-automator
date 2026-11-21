Back to [[Home]]. See also [[Guides/Routines|Routines]] (adding, trying, trusting and running one), [[Guides/Plugins and sign-ins|Plugins and sign-ins]].

A routine is a [Dotprompt](https://google.github.io/dotprompt/) file: YAML frontmatter that says what the routine may use and when it runs, then the prompt. The file name is the routine's name (`digest.prompt` is `digest`: lowercase letters, digits and dashes).

## An example
This one uses a GitHub MCP server you've added as `github` ([[Guides/Adding a plugin|Adding a plugin]]) and the built-in `notify`:

```yaml
---
description: Tells me each weekday morning which of my GitHub issues changed
automator.schedule: "0 9 * * 1-5"   # cron, local time; leave it out for a routine you run by hand
automator.staleness: 2h              # how late it may still run (e.g. after sleep)
automator.servers: [github, notify]  # the servers its tools come from
automator.parameters:
  - name: repo
    type: string
    default: my-org/my-repo
    description: The repository to look at
model: anthropic/claude-sonnet-5-5
tools:                               # the only tools the model may call
  - list_issues
  - get_issue
  - notify_me
---
{{role "system"}}
Look at the issues assigned to me in {{repo}} that changed since {{since}}.
If any did, notify me once with notify_me: a short title, one line per issue in the body.
{{> memories}}

Finish by replying with what changed, or that nothing did.
```

## Servers and tools
- `automator.servers` lists the servers the routine uses; `tools` lists, by name, every tool the model may call. Nothing else is reachable
- `procauto plugins list` shows the servers and `procauto mcp tools <server>` each one's tools (`--json` marks the read-only ones). `procauto routines trust <name> --check` shows a routine's tools split into writes and reads
- Tools that don't only read are **writes**: dry runs intercept them, and trusting the routine lists them separately

## Frontmatter
Plain Dotprompt keys:

| Key | |
|---|---|
| `description` | What it does |
| `model` | e.g. `anthropic/claude-sonnet-5-5`, `openai/gpt-5.5` on `codex`, or `openai-compat/qwen3:8b` on `api` for an [[Guides/Plugins and sign-ins#OpenAI-compatible servers (experimental, untested)\|OpenAI-compatible server]] (experimental, untested); left out, the backend's default (the `api` backend has none: name one). The `claude-code` backend runs only Claude models (`anthropic/…`), the `codex` backend only OpenAI ones (`openai/…`), the `api` backend `anthropic/…` and `openai-compat/…` ones |
| `config` | Model settings, e.g. `max_tokens: 4000` (the `api` backend only) |
| `tools` | The tool allowlist |
| `output` | The shape of its result (see [[#Structured output]]) |

The app's own keys, written dotted (`automator.servers:`, not a nested `automator:` block). A misspelled key is an error, not ignored:

| Key | Default | |
|---|---|---|
| `automator.servers` | | Servers it uses |
| `automator.schedule` | none | Cron, local time. Without one it runs only by hand |
| `automator.staleness` | `1h` | How late a missed scheduled run may still start; later, it's recorded as missed |
| `automator.timeout` | `20m` | How long a run may take before it's stopped and fails |
| `automator.effort` | `default` | How hard the model thinks: `default`, `low`, `medium`, `high`, `xhigh`, `max` (the same names on `claude-code` and `codex`, if the model has them; the `api` backend takes only `default`) |
| `automator.folder` | an empty temporary folder (one of its own that's kept, when it saves its session) | The folder it works in; its `CLAUDE.md` (`claude-code`) or `AGENTS.md` (`codex`) is added to the instructions |
| `automator.builtinTools` | `[]` | The agent CLI's own tools it may use, in that backend's names (see [[#Built-in tools]]); not on the `api` backend |
| `automator.session` | `discard` | `save` keeps the agent CLI session and records its ID, folder and backend with the run, so you can resume it from that folder (`claude --resume <id>`, `codex resume <id>`; without `automator.folder`, the routine gets a folder of its own for it) |
| `automator.concurrency` | `queue` | `queue` waits for other runs (one at a time); `parallel` runs alongside them. A routine never overlaps itself |
| `automator.parameters` | | Values set at install (see below) |
| `automator.platforms` | all | `[macos]`, `[linux]` or both |
| `automator.backends` | all | The backends it works with, e.g. `[claude-code]` for a routine using Claude Code's `builtinTools`, or `[claude-code, codex]` for one using `folder`, `session` or `effort`; a template that doesn't list the selected `llm.backend` won't install |
| `automator.plugins.<name>` | | A section for an in-process plugin that takes one (none ship with the app) |

From `automator.schedule` to `automator.concurrency`, each key is also a per-routine setting, and so is each parameter: the file gives the default, and `procauto settings set` overrides it without editing the file, e.g. `settings set routines.digest.timeout 45m` or `settings set routines.digest.params.repo my-org/other-repo` (see [[Guides/Settings|Settings]]).

## The prompt
Handlebars text. `{{role "system"}}` starts the instructions. Write it for an agent working alone: no one answers questions during a run.

Every routine gets these inputs:

| Input | Example |
|---|---|
| `{{today}}` | `Friday 2026-11-20` |
| `{{since}}` | `Thursday 2026-11-19`: the start of the previous workday (on Monday, Friday) |
| `{{now}}` | `2026-11-20T09:20:00+01:00`: the scheduled time, or when you started it |
| `{{trigger.kind}}` | `schedule`, `manual` or `backfill` |

A backfill (`run --date`) computes them for that day.

**Parameters** are values the routine needs that differ per person or project (a repository, a channel, a team), declared with a `name` (lowercase letters, digits and underscores), `type` (`string`, `int`, `bool`, `duration`, `enum` with `enum: [...]`, …), `description` and an optional `default`; one without a default is required. Use them as `{{name}}`. Installing a template asks for them; afterwards they're settings (`routines.<name>.params.<param>`).

**Partials**: a file `_<name>.prompt` in the routines folder is shared text any routine can include with `{{> name}}`. `{{> memories}}` is the app's: it brings in your [[Guides/Routines#Memories|memories]].

## How a run ends
The app gives every routine a `report_outcome` tool (you don't list it) and tells the model to call it at the end: `done`, `nothing_to_do` or `failed`, with a reason. A run fails if the routine reports `failed`, never reports, or a write's last attempt failed (e.g. a server refused the post). End your prompt with what the final reply should hold (e.g. "the message you posted, or why you didn't"): it's what run history shows.

## Structured output
For a result in a fixed shape instead of prose, declare it with Dotprompt's `output` (Picoschema):

```yaml
output:
  schema:
    posted: boolean, whether it posted
    message?: string, the message it posted
    reason?: string, why it didn't
```

The run fails if the result doesn't match; a valid one is stored with the run and counts as done.

## Notifying you
Add `notify` to `automator.servers` and `notify_me` to `tools`. The model can then send you a desktop notification: a `title`, a `body`, and `urgent` for something you must see now (it breaks through Focus). The plugin shows it itself, as the app: on macOS like the app's other notifications, on Linux through your desktop's notification service. Clicking it does nothing, so the prompt should say what to look at. It's a write, so dry runs intercept it, and the call is in the run's history.

## Working in a folder
With `automator.folder` set, the run works in that folder instead of an empty one, and its project instructions are added to the routine's: `CLAUDE.md` on `claude-code`, `AGENTS.md` on `codex` (the folder's own agent settings and hooks are never loaded). Combine it with `automator.builtinTools` to let the model read or change files there, e.g. a routine that checks a repository:

```yaml
automator.folder: ~/code/my-repo
automator.builtinTools: [Read, Grep, Glob]    # on codex: [shell]
```

## Built-in tools
Besides its servers' tools, a routine on an agent CLI backend may use the CLI's own tools, each allowed by name in `automator.builtinTools` (none by default). Each backend has its own names, and a name the selected backend doesn't have is a problem `procauto doctor` shows:

- **`claude-code`**: Claude Code's tool names, e.g. `[Read, Grep, Glob, Bash, Edit, WebSearch]`. File tools stay inside the run's folder. Dry runs get only `Read`, `Glob`, `Grep`, `WebSearch` and `WebFetch`
- **`codex`**: `shell` (Codex runs commands), `write` (commands and Codex's file edits may change files in the run's folder; without it Codex's sandbox is read-only), `network` (with `write`: commands may reach the network), `web_search`, `view_image` (looking at image files). Codex always has its own file-editing tool, which the read-only sandbox stops from writing. Dry runs drop `write` and `network`, so they run in the read-only sandbox, without network
