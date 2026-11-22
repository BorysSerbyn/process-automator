Back to [[Home]]. See also [[Guides/Routines|Routines]] (adding, trying, trusting and running one), [[Guides/Adding a plugin|Adding a plugin]].

A routine is a [Dotprompt](https://google.github.io/dotprompt/) file: YAML frontmatter that says what the routine may use and when it runs, then the prompt. The reference is in the app, so it matches the version installed:

```sh
procauto guide routines
```

It has a working example, every frontmatter key with its type, default and meaning, the inputs, each backend's models and built-in tools, and the steps once it's written. Claude reads the same guide through the app's MCP server ([[Guides/Using it from Claude|Using it from Claude]]). What follows is what the guide leaves out.

## Writing the prompt
Write it for an agent working alone. End it with what the final reply should hold (e.g. "the message you posted, or why you didn't"): it's what run history shows. A run fails if the routine reports `failed`, never reports, or a write's last attempt failed (a server refused the post), whatever the model says.

**Partials**: a file `_<name>.prompt` in the routines folder is shared text any routine includes with `{{> name}}`. `{{> memories}}` is the app's: it brings in your [[Guides/Routines#Memories|memories]].

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
With `notify` in `automator.servers` and `notify_me` in `tools`, the model can send you a desktop notification: a `title`, a `body`, and `urgent` for something you must see now (it breaks through Focus). Clicking it does nothing, so the prompt should say what to look at. It's a write: dry runs intercept it.

## Working in a folder
With `automator.folder`, a run works in that folder (an absolute path or `~/...`), and its `CLAUDE.md` (`claude-code`) or `AGENTS.md` (`codex`) is added to the instructions; the folder's own agent settings and hooks are never loaded. With `automator.builtinTools` the model can read or change files there, e.g. `[Read, Grep, Glob]` (on `codex`: `[shell]`). `automator.session: save` keeps the session so you can resume it from that folder (`claude --resume <id>`, `codex resume <id>`). `folder`, `session`, `builtinTools` and an `effort` other than `default` need an agent CLI backend (`claude-code` or `codex`).
