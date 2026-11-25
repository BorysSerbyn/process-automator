# Writing a routine

A Dotprompt file: YAML frontmatter, then a Handlebars prompt. The file name is the routine's name (lowercase letters, digits, dashes).

## Example
%{file:new-notes.prompt}

## Rules
- Dotprompt keys: `description`, `model`, `tools`, `output` (result schema), `config`
- App keys are dotted (`automator.servers:`); an unknown one is an error. Those marked settings can be overridden: `settings set routines.<routine>.<key> <value>`
- `tools` are names its servers offer: `%{command} mcp tools <server>`. `routines create` refuses unknown servers and tools
- A tool two of its servers offer (two accounts of one plugin: `automator.servers: [slack, slack-work]`) is named with its server, `server/tool` (`slack-work/slack_search`); an unqualified name offered by both is an error. The model sees `server/tool` as `server__tool`, described as that server's
- `{{role "system"}}` starts the instructions, `{{role "user"}}` the request; no one answers questions during a run
- The model ends by calling `%{outcomeTool}` (%{outcomes}), held to the routine's outcome contract (How a run ends); a run that never reports fails. Don't list it in `tools`
- Parameters: `name`, `type` (%{paramTypes}), `description`, optional `default`; used as `{{name}}`
- `{{> memories}}` adds my memories; `notify_me` (server `notify`) notifies me, and with `acknowledge: true` waits for me to acknowledge it (the app stays red until I do): use it for each thing I must act on

## How a run ends
Every routine says what done means and when there's nothing to do; `routines create`, `routines update` and template installs refuse one that doesn't:

```yaml
automator.outcome:
  done: My update is posted in today's Polly "Daily Stand-Up" thread.
  nothingToDo: [My update is already in today's thread]  # [] for none
  writes: [slack_send_message]  # optional: done needs a successful call of each
```

- Keep `nothingToDo` to the bare minimum: usually none, at most one. Models writing routines tend to list many, and each is one more way for a failure to pass as a success. A missing thread, an empty search or a read that returns only old items is a failure, not nothing to do
- The app adds a few lines from it at the end of the instructions, and refuses a report that breaks it (the model can correct it): `nothing_to_do` must name one of the cases, `done` needs each of `writes` to have succeeded (a dry run's intercepted call counts). A write whose last call failed fails the run, whatever the report
- Don't restate the contract in the prompt: the app adds it. Remove any rule there that contradicts it ("if the thread isn't there, don't post and say why" turns a failure into a success); at most one line on what's a failure for this routine in particular, when it isn't obvious
- Every call to its tools is kept with the run: arguments, when, and the result (up to %{callCap} KiB a call, %{runCap} KiB a run) or error: `%{command} runs show <id>`. An agent CLI's built-in tools (Read, Bash…) aren't
- A routine written before contracts were required still runs, reporting only done or failed; `routines check` and `doctor` warn about it

## Keys
%{table:fields}

## Inputs
%{table:inputs}

## Backends and models
%{table:backends}

Built-in tools:
%{table:builtins}

## After writing it
1. `%{command} routines create <name> --from <file>` (MCP: `routines_create` with `source`): starts paused and untrusted
2. `%{command} run <name> --dry-run`: writes intercepted, no trust needed
3. `%{command} routines trust <name> --resume`: the person turns it on, in a terminal (not an MCP tool)
4. `%{command} routines check <name>`; edit with `%{command} routines update <name> --from <file>`
