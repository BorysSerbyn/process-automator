Back to [[Home]]. Details live in the [[Spec/Requirements/Functional/Functional Requirements|requirements]] and the [[Spec/Technical Decisions|decisions]].

## What it is
- **A blank canvas for scheduled LLM routines**: a routine is one Dotprompt file on my device saying what the model should do, which tools it may use and when it runs. The app ships with no routines, no templates and no bundled integrations
- **Tools come from MCP servers**: any server I add (remote or local, or a folder I wrote), plus `notify` (`notify_me`), a plugin compiled into the app
- **Three model backends**: Claude Code on my Claude plan (`claude-code`), OpenAI's Codex CLI on my Codex sign-in (`codex`, experimental and untested), or an Anthropic API key (`api`)
- **A scheduler** that starts at login, catches up after sleep within each routine's staleness, and records every due run
- **Guardrails**: a tool allowlist per routine, dry runs that intercept every write, and trust: a routine runs for real only while what it can reach is what I approved
- **Frontends over one core**: the `procauto` CLI (the API), automator-ui on macOS, and Claude or any MCP client through `procauto mcp serve`
- **Around runs**: run history, memories, notifications, a usage limit per backend, `doctor`, templates to share routines, a guided `setup`
- **Distribution**: prebuilt, verified releases for macOS and Linux, one-command install, update and uninstall, a private Homebrew tap, and installing through an LLM (`AGENTS.md`)

## What it isn't
- Not a chat assistant or an open-ended agent: routines are fixed prompts run unattended, with no one answering questions mid-run
- Not tied to any service: the engine knows no specific service; integrations are MCP servers I add
- No menu bar app on Linux: the CLI and the scheduler only
- No Windows support (the core cross-compiles for it, nothing more)
- Memories are mine: routines never write them

## Not in scope
- **Event triggers**: routines started by something happening (a new email, a file appearing), not only by time. Runs already carry what triggered them, so this would be an addition
- **Install links**: one-click links that pre-fill installing a template or a plugin, still reviewed and approved by me
- **Code-side output validation**: code checking a routine's actions against rules declared in its file; output is left to the model's judgement, corrected with memories
