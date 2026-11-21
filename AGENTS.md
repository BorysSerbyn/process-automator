# Installing Process Automator (for an AI agent)

Follow this when someone asks you to install process-automator for them. (Working on the code instead? See `docs/Guides/Developing.md`.)

## What the app does
Process Automator runs **routines**: prompt files that a model (Claude, through Claude Code or the API; or OpenAI's, through the Codex CLI) runs on a schedule in the background, with tools from **plugins** (MCP servers: `notify`, which ships with the app, plus any MCP server they add). Out of the box it does nothing: anything a prompt plus tools can do, a routine can do. Around that it has:
- **routines:** create, edit, run now or as a dry run (nothing posted), pause, stop, delete; schedules in plain words; run history with every tool call, output and cost;
- **plugins:** add any MCP server (remote, local, or a folder they wrote), try it before it's installed, sign in;
- **settings:** app-wide and per routine (schedule, model, effort, timeout, working folder, which tools it may use, ...);
- **memories** routines read, desktop **notifications** routines send, **templates** to start from, an **LLM budget** that keeps scheduled runs from overspending, and **guardrails**: a routine runs for real only once trusted with what it can reach;
- a menu bar app on macOS (status, routines, settings, setup), and `doctor` to explain anything red.

**All of it is yours through the app's MCP server**, and it's very powerful: every CLI command is a tool, so through it you can do basically anything the person can, such as writing and testing new routines, adding plugins, reading run history, changing settings and running routines. On the `claude-code` backend, setup connects Claude Code to it (the `mcp` step below); for another MCP client, its stdio command is `procauto mcp serve`.

Tools that change things are marked so the client asks first, and sign-ins happen in the person's browser, never through you. A few things stay with the person, by design: trusting a routine to run for real, uninstalling the app, and the scheduler itself.

## Installing it
Your job is to run the commands and to ask the person, never to choose for them. Everything below runs without an interactive terminal: read `--json` output, and pass `--yes` only after they've answered. Ask one question at a time, in plain words, quoting what the app says (each setup step's `ask` and `status`).

## 1. Check the prerequisites
- **OS**: macOS 14 or later on Apple silicon (not Intel Macs), or Linux (x86-64 or ARM64). Nothing else is supported; stop and say so.
- **GitHub access**: the repository is private. `gh auth status` must show a GitHub account that can read it (any of the accounts signed in: install.sh and `procauto update` find the one that can, so the active account needn't be); otherwise ask them to run `gh auth login` (or set `GH_TOKEN`). Check with `GH_TOKEN=$(gh auth token -u <account>) gh repo view BorysSerbyn/process-automator` for each account; don't switch their active account.
- **Linux only**: `secret-tool` (package `libsecret-tools`) for keeping sign-ins, and a systemd user session for starting the scheduler at login. Install the package only if they agree.
- **Already installed?** If `procauto version --json` works, the app is there: offer `procauto update` instead, and go on to step 3.

Don't install the model backend's tools or the apps behind MCP servers (Claude Code, the Codex CLI, local servers) yourself; setup says when one is needed, and you ask.

## 2. Install
From a checkout of the repository (clone it first if you haven't):

```sh
./install.sh --yes
```

It downloads the latest release for this machine, checks its checksum (and on macOS its signature), installs it (macOS: `~/Applications/Process Automator.app`; Linux: `~/.local/lib/process-automator`), links `procauto` into `~/.local/bin`, and starts the scheduler (and on macOS the menu bar app) now and at login. If a check fails, show them the error; don't work around it.

**No published release for this machine** (install.sh says so): build it from the checkout instead. On macOS:
1. Prerequisites, each with their go-ahead: the Xcode command line tools (`xcode-select --install`: *they* confirm the dialog), Homebrew, then `make deps` (Go and the other build tools).
2. A code-signing identity named `Process Automator Dev`, which keeps macOS permission grants across rebuilds. *They* create it in Keychain Access, following `docs/Guides/Getting started.md` → "Building it from the checkout"; check with `security find-identity -v -p codesigning`.
3. `make install`. It builds the core and the app, installs them as above, and remembers this checkout: `procauto update` builds newer tagged releases from it until releases are published.

On Linux: Go (from their package manager, with their go-ahead), then `make package-linux LINUX_ARCH=<amd64|arm64>` and `./install.sh --yes --from build/dist/<the .tar.gz it made>`.

If `~/.local/bin` isn't on their PATH, use `~/.local/bin/procauto` below and tell them how to add it.

## 3. Walk them through setup
```sh
procauto setup --json
```

lists every step in order (`templates`, `backend`, `usage` when needed, `mcp` on the `claude-code` backend, a `routine` to fix, `sign-in`, `turn-on`), each with `done`, `optional`, `auto`, `status`, `ask` and `commands`. It changes nothing. For each step that isn't `done`, in order:

1. If it's `auto`, run its `commands` without asking and tell them what you did. Otherwise tell them its `status`, and ask its `ask`.
2. Run its `commands` with their answers in place of the `<placeholders>` (a trailing `# comment` says when a command applies).
3. Run `procauto setup --json` again: answers can add steps (installing a template adds its sign-ins and its turn-on step; choosing `claude-code` adds `mcp`).

What each kind needs:
- **`templates`** (optional): run `procauto templates list --json` (there may be none: then offer to write their first routine with them instead, or skip). Offer only templates with `supportsBackend`: the others need another backend, and installing them is refused.
  1. **Projects**, if any template has a `project`: ask, on its own, which of those projects they work on, naming every one, even when there's only one, plus "none of these". Show no templates yet, and don't assume the answer.
  2. **Templates**: list each template as its own choice, with its id, description and parameters: those of their projects, then those with no project. Never bundle templates into one choice. Templates with the same name install as the same routine, so they're alternatives: offer the one of their project. **None are picked by default**: install only the ones they name, asking for each parameter's value (or keeping its default if they say so): `procauto templates install <tags>/<name> [--set <param>=<value>]...`.
- **`backend`**: ask which backend runs routines (the `ask` explains each: `claude-code` uses their Claude subscription through the Claude Code CLI; `codex` uses their Codex sign-in (a ChatGPT plan or an OpenAI API key) through OpenAI's Codex CLI; `api` uses an Anthropic API key), then `procauto settings set llm.backend <choice>`. If it then needs a key, `procauto auth login anthropic --browser` opens a page in their browser where *they* enter it. On `codex`, if the step's `status` says Codex isn't installed or signed in, ask them to install it and run `codex login` themselves (it signs in in their browser), then check again.
- **`mcp`** (`auto`, only on the `claude-code` backend): run `procauto mcp connect`, without asking. It registers the app's MCP server with Claude Code at user scope, so `/mcp` lists `procauto` in every folder. It's required: don't skip it. Tell them a Claude Code session that's already running (yours too) needs a restart to see it.
- **`usage`** (only on the `claude-code` backend, when Claude Code's `/usage` can't be read): ask whether to read the plan's usage from their Claude Code status line instead; if yes, `procauto usage statusline install` (their status line keeps showing what it shows now).
- **`routine`**: a routine that won't run; `procauto doctor --json` says why and how to fix it. Explain it; fix it only with their go-ahead.
- **`sign-in`**: `procauto auth login <server> --browser` opens their browser to sign in (or to a local page for a key). They do it there; secrets never pass through you. Wait for the command to finish.
- **`turn-on`**: one step per routine: trusting it with what it can reach and running it on its schedule. If its commands include `--check`, run `procauto routines trust <name> --check --json` and show them what the routine can reach (its servers, and its tools split into writes and reads), then ask its `ask` (it gives the schedule in words). Only if they agree: `procauto routines trust <name> --yes --resume`, which trusts it and resumes it together. A trusted routine that's only paused asks just about its schedule, and that step is always optional: pausing is their choice.

Setup is finished when `procauto setup --json` says `"done": true`. Steps they skip stay listed, and `procauto setup` can be run again any time.

## 4. Finish
Tell them:
- what's installed and running (`procauto routines list`, and on macOS the hourglass in the menu bar);
- `procauto doctor` explains a red light; `procauto update` installs new releases (the menu offers them too); `procauto uninstall` removes the app (`--purge` also removes their config, routines, history, logs and secrets);
- you (or any MCP client) can do basically anything with the app from now on through its MCP server (on `claude-code`, setup connected it to Claude Code; after a restart `/mcp` lists `procauto`): offer to help them write their first routine.
