# Writing a routine

A routine is a Dotprompt file: YAML frontmatter that says what the routine may use and when it runs, then the prompt the model runs. This is everything needed to write one that `%{command} routines create` accepts and that runs.

## An example
This routine uses the notes server from `%{command} guide plugins` and notifies me through the `notify` plugin. Saved as `new-notes.prompt`, it's the routine `new-notes`:

%{file:new-notes.prompt}

## The file
- **Frontmatter** between two `---` lines, in YAML: Dotprompt's own keys, and the app's, written dotted (`automator.servers: [...]`, never a nested `automator:` block). An unknown or misspelled `automator.*` key is an error, not ignored
- **The prompt** after it, in Handlebars. `{{role "system"}}` starts the instructions; `{{role "user"}}` starts the request (agent CLI backends take only these two). Write it for an agent working alone: no one answers questions during a run
- **The name** is the file name without `.prompt`: lowercase letters, digits and dashes. `routines create <name>` takes it, and the file lands in the routines folder (`%{command} paths`) as `<name>.prompt`

Dotprompt's own keys:
- `description`: what it does, in a line
- `model`: the model, as `<provider>/<model>` (see Models and backends); left out, the backend's default
- `config`: model settings, e.g. `max_tokens: 4000` (only where Genkit runs the model: `claude-api` and `local`)
- `tools`: the tools the model may call, by name (see Servers and tools)
- `output`: the shape of its result (see Structured output)

## The app's keys
Each is `automator.<key>` in the frontmatter. Those marked as settings are per-routine settings too: the file gives the default, and `%{command} settings set routines.<routine>.<key> <value>` overrides it without editing the file.

%{table:fields}

## Servers and tools
- `automator.servers` lists the servers its tools come from: the app's plugins (below) and the MCP servers added with `%{command} plugins add` (`%{command} plugins list` shows them all; `%{command} guide plugins` adds more)
- `tools` lists every tool the model may call, by the name its server gives it: `%{command} mcp tools <server>` lists them (`--json` marks the read-only ones). Nothing else is reachable
- Tools that don't only read are **writes**: dry runs intercept them, and trusting the routine lists them apart
- `routines create` and `routines update` refuse a server nothing provides and a tool none of its servers offers (or two do). A server that can't be asked yet (not signed in) leaves its tools unchecked, with a warning; `routines check` checks them later
- Don't list `%{outcomeTool}`: every routine gets it (see How a run ends)
- `automator.plugins.<name>` is a section of settings for an in-process plugin that takes them from routines; it must be one of `automator.servers`, and the plugin's own docs say what goes in it

Plugins this app has built in:

%{table:plugins}

## Parameters
`automator.parameters` declares values that differ per person or project (a repository, a channel), each with:
- `name`: lowercase letters, digits and underscores, starting with a letter; not an input's name (below)
- `type`: %{paramTypes} (`enum` needs `enum: [...]`)
- `description`, and an optional `default`: one without a default is required

The prompt uses one as `{{name}}`. Installing a template asks for each; afterwards each is a setting, `routines.<routine>.params.<name>`.

## Inputs, memories and partials
Every routine's prompt gets these inputs, as of the day it runs for (a backfill, `run <routine> --date`, gets that day's):

%{table:inputs}

- `{{> memories}}` brings in my memories (every routine's, and this routine's own); put it in the instructions
- A file `_<name>.prompt` in the routines folder is a partial: shared text any routine includes with `{{> name}}`

## Models and backends
The `llm.backend` setting picks what runs every routine; a routine's `model` must be one the backend runs:

%{table:backends}

`automator.backends` lists the backends a routine works with (left out, all), e.g. `[claude-code]` for one that uses Claude Code's built-in tools; `automator.platforms` the platforms it runs on: %{platforms} (left out, all). `automator.effort` other than `default` needs an agent CLI backend (`claude-code` or `codex`).

## Built-in tools
On an agent CLI backend a routine may also use the CLI's own tools, each allowed by name in `automator.builtinTools` (none by default), in that backend's names. A name the backend doesn't take is a problem `%{command} doctor` shows. Dry runs keep only those that can't change anything:

%{table:builtins}

On `claude-code`, file tools stay inside the run's folder.

## Folder and session
- `automator.folder` is the folder a run works in (an absolute path, or one starting with `~/`); its project instructions (`CLAUDE.md` on `claude-code`, `AGENTS.md` on `codex`) are added to the routine's, never its agent settings or hooks. Combine it with `automator.builtinTools` to let the model read or change files there, e.g. `[Read, Grep, Glob]` (on `codex`: `[shell]`)
- `automator.session: save` keeps the agent CLI session, and records its ID, folder and backend with the run, so it can be resumed from that folder (`claude --resume <id>`, `codex resume <id>`)
- Both need an agent CLI backend

## How a run ends
Every routine gets a `%{outcomeTool}` tool, and is told to call it once at the end: %{outcomes}, with a reason. A run fails if the routine reports `failed`, never reports, or a write's last attempt failed (a server refused the post), whatever the model says. End the prompt with what the final reply should hold (e.g. "the message you posted, or why you didn't"): it's what run history shows.

## Structured output
For a result in a fixed shape instead of prose, declare it with Dotprompt's `output` (Picoschema):

```yaml
output:
  schema:
    posted: boolean, whether it posted
    message?: string, the message it posted
    reason?: string, why it didn't
```

The run fails if the result doesn't match; a valid one is stored with the run.

## Notifying me
With the `notify` plugin (when the app has it: see Servers and tools), add `notify` to `automator.servers` and `notify_me` to `tools`. The model can then send me a desktop notification: a `title`, a `body`, and `urgent` for something I must see now (it breaks through focus modes). Clicking it does nothing, so the text should say what to look at. It's a write: dry runs intercept it.

## After writing it
1. `%{command} routines create <name> --from <file>` (over MCP: `routines_create` with the file's text as `source`). It's validated first, and starts **paused and untrusted**
2. `%{command} run <name> --dry-run`: runs it with every write intercepted, to see what it would do. Dry runs need no trust
3. `%{command} routines trust <name> --resume`: shows what it can reach (its servers, writes and reads), then trusts it and runs it on its schedule. Trusting is the person's decision, made in a terminal or the app: it isn't an MCP tool
4. `%{command} routines check <name>`: checks it would run (file, settings, servers, tools, model) without calling the model
5. To change it: `%{command} routines update <name> --from <file>` (its settings are kept). Changing its servers, tools, built-in tools, plugin sections or folder takes its trust away until it's trusted again; its prompt, schedule and model don't

`%{command} runs <name>` and `%{command} runs show <id>` show how its runs went; `%{command} schedule build --days mon,fri --at 09:00` writes a schedule.
