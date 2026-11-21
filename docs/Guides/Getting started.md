Back to [[Home]]. Next: [[Guides/Routines|Routines]], [[Guides/The menu bar app|The menu bar app]].

## What it is
Process Automator runs **routines**: prompt files that a model (Claude) runs on a schedule in the background, with the tools you allow. Out of the box it does nothing; the routines you add give it a purpose (e.g. a morning digest of your open issues, a weekly report, a stand-up draft).

- **Routines** are `.prompt` files in a routines folder on your machine. You write them ([[Guides/Writing a routine|Writing a routine]]), install them from templates your team shares, or ask Claude to write them
- **Plugins** are MCP servers that give routines their tools: any server you add ([[Guides/Adding a plugin|Adding a plugin]]), plus `notify` (desktop notifications), which ships with the app ([[Guides/Plugins and sign-ins|Plugins and sign-ins]])
- **The model backend** runs routines: `claude-code` (Claude Code on your Claude plan), `codex` (OpenAI's Codex CLI on your Codex sign-in; experimental, untested) or `api` (an Anthropic API key, paid per token; or, experimental and untested, models on an OpenAI-compatible server such as Ollama)
- **The scheduler** starts at login and runs enabled routines on their schedules
- **The `procauto` CLI** does everything; every command has `--help`
- **The menu bar app** (macOS) shows whether all is well and lets you manage routines without a terminal ([[Guides/The menu bar app|The menu bar app]])
- **The MCP server** gives Claude everything the CLI does ([[Guides/Using it from Claude|Using it from Claude]])

## Requirements
- macOS 14 or later on a Mac with Apple silicon (Intel Macs aren't supported), or Linux (x86-64 or ARM64)
- A GitHub account that can read the private `process-automator` repository, signed in with the GitHub CLI (`gh auth login`) or a token in `GH_TOKEN`
- Linux only: `secret-tool` (package `libsecret-tools`) to keep sign-ins, and a systemd user session to start the scheduler at login

## Install
From a checkout of the repository:

```sh
./install.sh
```

Or without a checkout (`<owner>` is the GitHub account that hosts the repository):

```sh
gh api repos/<owner>/process-automator/contents/install.sh -H 'Accept: application/vnd.github.raw' | bash
```

It downloads the latest release for your machine, checks its checksum (and on macOS its signature), installs it, links `procauto` into `~/.local/bin`, starts the scheduler (and on macOS the menu bar app) now and at login, then offers to run setup. If `~/.local/bin` isn't on your `PATH`, add it.

| | Where the app goes |
|---|---|
| macOS | `~/Applications/Process Automator.app` |
| Linux | `~/.local/lib/process-automator` |

`./install.sh --help` lists its options (`--version`, `--from <archive>`, `--yes`).

### Building it from the checkout
When there's no published release for your machine, `install.sh` says so. Build it instead:

- **macOS**:
  1. Install the Xcode command line tools (`xcode-select --install`) and Homebrew, then `make deps` (Go and the other build tools)
  2. Create a code-signing certificate once, so macOS keeps its permission grants across rebuilds: Keychain Access › Certificate Assistant › Create a Certificate…, named `Process Automator Dev`, Identity Type **Self-Signed Root**, Certificate Type **Code Signing**. `security find-identity -v -p codesigning` should list it
  3. `make install`: builds, installs and starts the app as above. `procauto update` then builds newer release tags from this checkout
- **Linux**: install Go, then `make package-linux LINUX_ARCH=amd64` (or `arm64`) and `./install.sh --from build/dist/<the .tar.gz it made>`

### Homebrew
If you've been given access to the private Homebrew tap: `brew install --cask process-automator` on macOS, `brew install process-automator` on Linux, with `HOMEBREW_GITHUB_API_TOKEN` set to a token that can read the repository. An app installed with Homebrew is removed with `brew`; on Linux it's also updated with `brew upgrade`.

### Through an AI agent
Tell Claude Code (or another coding agent) to "find the process-automator repo and install it". It follows `AGENTS.md` in the repository: it installs the app, then walks you through setup, asking you for each choice instead of choosing for you.

## Set it up
Setup takes you through what the app needs, in order:

1. **Templates** (optional): if any templates are made for particular projects, which projects you work on; then which templates to install. None are picked by default, and with no templates available the step has nothing to offer
2. **Model backend**: `claude-code`, `codex` or `api`, and its sign-in ([[Guides/Plugins and sign-ins#Model backend|Model backend]])
3. **Plan usage** (only if the app can't read your Claude plan's usage from Claude Code): reading it from your Claude Code status line instead ([[Guides/Settings#Usage limit|Usage limit]])
4. **Claude Code** (on the `claude-code` backend): setup connects Claude Code to the app's MCP server, without asking, so `/mcp` lists it in every folder ([[Guides/Using it from Claude|Using it from Claude]])
5. **Routines to fix**: any routine that can't run, and why
6. **Sign-ins**: each server your routines use. One that needs something set up first walks you through it before asking for anything ([[Guides/Plugins and sign-ins|Plugins and sign-ins]])
7. **Turn on routines**: what each routine can reach, for you to approve; one yes trusts it and runs it on its schedule ([[Guides/Routines#Turn it on|Turn it on]])

Run it in a terminal with `procauto setup`, which asks before each step and skips what's done, or on macOS open **Finish Setup…** from the menu bar (or **Setup** in the Routines window), which shows every step with a button for each. Run it again any time, e.g. after adding a routine. Until the required steps are done, the menu bar dot is red with "Setup isn't finished".

## Update
`procauto update` installs the latest release if it's newer (`--check` only reports; `update progress` says what one installing is doing). It verifies the download like the installer, replaces the app where it is and restarts it. On macOS the menu offers it too (**Update to X…**, or **Check for Updates**), and shows its progress. Your routines, settings and sign-ins are kept. If `gh` is signed in to several GitHub accounts, it uses the first that can read the releases, so the active one needn't be.

## Uninstall
`procauto uninstall` removes the app, its `procauto` link and its login items. Your config, routines, history, logs and secrets stay, unless you add `--purge`.

## Where things are
`procauto paths` shows where the app keeps its config file, routines folder, run status, history, notifications and templates. The data folder is `~/Library/Application Support/process-automator/` on macOS and `~/.config/process-automator/` on Linux; on macOS the scheduler logs to `~/Library/Logs/process-automator/scheduler.log`.

## On Linux
The CLI and the scheduler work as on macOS, with no menu bar app: use the CLI, or Claude through the MCP server. Only templates that run on Linux are offered. The scheduler is the systemd user unit `procauto-scheduler.service` (logs: `journalctl --user -u procauto-scheduler`). Notifications are recorded but not shown on screen: `procauto notifications list` lists them.
