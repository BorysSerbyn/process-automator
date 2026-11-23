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
- The model ends by calling `%{outcomeTool}` (%{outcomes}); a run that never reports fails. Don't list it in `tools`
- Parameters: `name`, `type` (%{paramTypes}), `description`, optional `default`; used as `{{name}}`
- `{{> memories}}` adds my memories; `notify_me` (server `notify`) notifies me

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
