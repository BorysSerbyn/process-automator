Back to [[Home]]. See also [[Guides/Routines|Routines]], [[Spec/Requirements/Functional/Agentic Functional Requirements|Agentic Functional Requirements]].

The app is also an MCP server: every `procauto` command is a tool. Connected to Claude Code, Claude Desktop or any MCP client, Claude can do almost anything you can with the app, by asking it in plain words.

## Connect it
On the `claude-code` backend, setup connects Claude Code for you: `/mcp` lists `procauto` in every folder. To do it yourself:

```sh
procauto mcp connect     # claude mcp add --scope user procauto -- ~/.local/bin/procauto mcp serve
```

For another MCP client, add a stdio server whose command is `procauto mcp serve` (use the full path, `~/.local/bin/procauto`, if the client doesn't find it). `mcp serve --read-only` serves only the tools that change nothing.

After `procauto update`, reconnect the server so the client sees new or changed tools.

## What Claude can do
- Write a routine from your description (it reads `procauto guide routines` first): pick its servers and tools, set its schedule and parameters, create it, check it and dry-run it to show you what it would do
- Install templates, add plugins and try them, change settings
- Read your routines, plugins and their tools, run history and outcomes, and explain why something failed (`doctor`)
- Run, start, stop, pause and resume routines, programs too; add and remove memories
- Show hooks and watchers, fire a hook with a sample event (a dry run first), pause and stop them
- Show which permissions things need and what the scheduler found (`permissions list`), and have macOS ask you for one (`permissions request`): the prompt is yours to answer

## What stays with you
- **Approval**: tools that change something are marked so the client asks you before calling them
- **Trust**: Claude can't trust a routine. Routines it creates start paused and untrusted, and only run for real once you've trusted them ([[Guides/Routines#Trust it|Trust it]])
- **Programs**: Claude can't add one ([[Guides/Writing a program|Writing a program]])
- **Hooks and watchers**: Claude can't add, change or trust one ([[Guides/Writing a hook|Writing a hook]])
- **Secrets**: a sign-in asked for by Claude opens in your browser (an OAuth page, or a local page where you type a key). Keys and tokens never pass through the chat
- **Uninstalling** and the scheduler itself are terminal-only

Everything Claude changes through the server is logged in `mcp-changes.jsonl` in the data folder (`procauto paths` shows where).

## Inside routines
Routines don't get this server: it's the app's interface for you and Claude, not a plugin. A Go plugin compiled into a build can read it (`Host.ConnectApp`, read-only tools only).
