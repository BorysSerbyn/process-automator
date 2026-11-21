Process Automator runs scheduled LLM **routines**: prompt files that a model runs in the background, on a schedule, with the tools I allow from MCP servers. A Go core and the `procauto` CLI do the work; on macOS, automator-ui (a menu bar status indicator with settings, routines and setup windows) sits on top, and on Linux a tray (automator-tray). On its own the app does nothing: the routines I add give it a purpose.

## Guides
Everything needed to use the app, in reading order:
- [[Guides/Getting started|Getting started]] (what it is; install, setup, update, uninstall)
- [[Guides/The menu bar app|The menu bar app]] (the status dot, its menu and windows, notifications; the Linux tray)
- [[Guides/Routines|Routines]] (templates, dry runs, trust, turning them on, running, history, memories)
- [[Guides/Writing a routine|Writing a routine]] (the routine file: servers, tools, schedule, settings, inputs, outcome, structured output)
- [[Guides/Plugins and sign-ins|Plugins and sign-ins]] (the model backend, the plugin that ships with the app, signing in)
- [[Guides/Adding a plugin|Adding a plugin]] (any MCP server: remote or local, OAuth, API key or public, or a folder)
- [[Guides/Settings|Settings]] (app-wide and per routine, the usage limit, start at login)
- [[Guides/Using it from Claude|Using it from Claude]] (the MCP server)
- [[Guides/Troubleshooting|Troubleshooting]] (a red dot, `doctor`, run history, logs)
- [[Guides/Developing|Developing]] (building, testing, releasing, and building another app on it)

## Spec
What the app is, and why it's built the way it is:
- [[Spec/Scope|Scope]] (what it is and isn't, in a page)
- [[Spec/Requirements/Functional/Functional Requirements|Functional Requirements]]
- [[Spec/Requirements/Functional/Agentic Functional Requirements|Agentic Functional Requirements]] (Claude as a frontend; the agent inside a routine)
- [[Spec/Requirements/Technical/Technical Requirements|Technical Requirements]]
- Distribution: [[Spec/Requirements/Functional/Distribution|functional]] · [[Spec/Requirements/Technical/Distribution|technical]]
- [[Spec/Technical Decisions|Technical Decisions]]
