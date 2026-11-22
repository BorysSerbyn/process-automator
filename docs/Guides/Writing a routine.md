Back to [[Home]]. See also [[Guides/Routines|Routines]] (adding, trying, trusting and running one), [[Guides/Adding a plugin|Adding a plugin]].

A routine is a [Dotprompt](https://google.github.io/dotprompt/) file: YAML frontmatter that says what the routine may use and when it runs, then the prompt. The file name is the routine's name (`digest.prompt` is `digest`).

How to write one is in the app itself, so it always matches the version installed:

```sh
procauto guide routines
```

It has a complete example, and covers the file's layout, every frontmatter key with its type, default and meaning, servers and tools, parameters, the inputs (`{{today}}`, `{{since}}`, ...), memories and partials, the models each backend runs, each backend's built-in tools, folders and sessions, how a run ends, structured output, notifying you, and what to do once it's written (create it, dry-run it, trust it, check it). `--json` gives the same as sections, with its tables as data.

Claude reads the same guide through the app's MCP server, so it can write routines for you ([[Guides/Using it from Claude|Using it from Claude]]).
