<p align="center">
  <img src="automator-ui/Resources/Logo/AppIcon-1024.png" alt="Process Automator logo: an hourglass drawn as one stroke running into a green status dot" width="160">
</p>

# process-automator

LLM routines on a schedule, and hooks on events, for macOS and Linux. Describe a task in a prompt file, choose when it runs (a time, or something happening: a meeting starting, a new email), and a model does it in the background with the tools you allow it, from any MCP server. A program can do the work instead, when it needs no model. On its own the app does nothing; your routines and hooks give it a purpose.

## Install

```sh
curl -fsSL https://github.com/BorysSerbyn/process-automator/releases/latest/download/install.sh | bash
```

or `./install.sh` from a checkout. No GitHub account needed. Or ask Claude Code (or another coding agent) to "find the process-automator repo and install it": it follows [`AGENTS.md`](AGENTS.md) and walks you through setup. See [Getting started](docs/Guides/Getting%20started.md) for requirements, building from source, updating and uninstalling.

## Documentation

[`docs/`](docs/Home.md) (an Obsidian vault): guides for using it, writing routines, hooks and plugins, and the spec. Working on the code: [Developing](docs/Guides/Developing.md).
