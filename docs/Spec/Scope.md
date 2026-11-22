Back to [[Home]]. Details live in the [[Spec/Requirements/Functional/Functional Requirements|requirements]] and the [[Spec/Technical Decisions|decisions]].

## What it is
- **A blank canvas for scheduled LLM routines**: a routine is one Dotprompt file on my device saying what the model should do, which tools it may use and when it runs. The app ships with no routines, no templates and no bundled integrations
- **Programs, as a second kind of routine**: an executable plus a description file (its schedule, whether it writes, its secrets), run instead of a model. They share scheduling, run, stop, pause, history and trust with LLM routines and show up in the same lists; "routine" with no kind means an LLM routine, everywhere
- **Tools come from MCP servers**: any server I add (remote or local, or a folder I wrote), plus `notify` (`notify_me`), a plugin compiled into the app
- **Four model backends**: Claude Code on my Claude plan (`claude-code`), an Anthropic API key (`claude-api`), my own OpenAI-compatible model server, local or hosted (`local`, experimental and untested), or OpenAI's Codex CLI on my Codex sign-in (`codex`, experimental and untested)
- **A scheduler** that starts at login, catches up after sleep within each routine's staleness, and records every due run
- **Guardrails**: a tool allowlist per routine, dry runs that intercept every write, and trust: a routine runs for real only while what it can reach is what I approved; a program runs only while its executable is the one I approved
- **Frontends over one core**: the `procauto` CLI (the API), automator-ui on macOS, a tray on Linux, and Claude or any MCP client through `procauto mcp serve`
- **Around runs**: run history, memories, notifications, a usage limit per backend, `doctor`, templates to share routines, a guided `setup`
- **Distribution**: prebuilt, verified releases for macOS and Linux, one-command install, update and uninstall, a private Homebrew tap, and installing through an LLM (`AGENTS.md`)

## What it isn't
- Not a chat assistant or an open-ended agent: routines are fixed prompts run unattended, with no one answering questions mid-run
- Not tied to any service: the engine knows no specific service; integrations are MCP servers I add
- On Linux, no days-and-time picker for a routine's schedule: it's edited as its cron setting
- No Windows support (the core cross-compiles for it, nothing more)
- Memories are mine: routines never write them

## Not in scope
- **Event triggers**: routines started by something happening (a new email, a file appearing), not only by time. Runs already carry what triggered them, so this would be an addition
- **Programs compiled into a build**: programs a plugin registers as Go functions, beside the executables I add
- **A program's result starting an LLM routine**: a program handing what it found to a routine to act on
- **Install links**: one-click links that pre-fill installing a template or a plugin, still reviewed and approved by me
- **Code-side output validation**: code checking a routine's actions against rules declared in its file; output is left to the model's judgement, corrected with memories
