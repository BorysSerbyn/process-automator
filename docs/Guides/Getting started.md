Back to [[Home]]. Next: [[Guides/Routines|Routines]], [[Guides/The menu bar app|The menu bar app]].

## What it is
Process Automator runs **routines**: prompt files that a model (Claude) runs on a schedule in the background, with the tools you allow. Out of the box it does nothing; the routines you add give it a purpose (e.g. a morning digest of your open issues, a weekly report, a stand-up draft).

- **Routines** are `.prompt` files in a routines folder on your machine. You write them ([[Guides/Writing a routine|Writing a routine]]), install them from templates your team shares, or ask Claude to write them
- **Plugins** are MCP servers that give routines their tools: any server you add ([[Guides/Adding a plugin|Adding a plugin]]), plus `notify` (desktop notifications), which ships with the app ([[Guides/Plugins and sign-ins|Plugins and sign-ins]])
- **The model backend** runs routines: `claude-code` (Claude Code on your Claude plan), `claude-api` (an Anthropic API key, paid per token), `local` (your own model server, such as Ollama; experimental, untested) or `codex` (OpenAI's Codex CLI on your Codex sign-in; experimental, untested)
- **The scheduler** starts at login and runs enabled routines on their schedules
- **The `procauto` CLI** does everything; every command has `--help`
- **The menu bar app** (macOS) shows whether all is well and lets you manage routines without a terminal; on Linux, **the tray** shows the same status, menu and notifications ([[Guides/The menu bar app|The menu bar app]])
- **The MCP server** gives Claude everything the CLI does ([[Guides/Using it from Claude|Using it from Claude]])

## Requirements
- macOS 14 or later on a Mac with Apple silicon (Intel Macs aren't supported), or Linux (x86-64 or ARM64)
- Linux only: `secret-tool` (package `libsecret-tools`) to keep sign-ins, and a systemd user session to start the scheduler at login. The tray needs a desktop with a system tray (StatusNotifierItem): on GNOME, the AppIndicator extension, which Ubuntu enables by default

## Install
From a checkout of the repository:

```sh
./install.sh
```

Or without a checkout:

```sh
curl -fsSL https://github.com/BorysSerbyn/process-automator/releases/latest/download/install.sh | bash
```

Neither needs a GitHub account or the GitHub CLI. (An app built on Process Automator whose releases are private is different: installing and updating it need a GitHub sign-in that can read them, with `gh auth login` or a token in `GH_TOKEN`.)

It downloads the latest release for your machine, checks its checksum (and on macOS its signature), installs it, links `procauto` into `~/.local/bin`, starts the scheduler, and the menu bar app on macOS or the tray in a Linux desktop session, now and at login, then offers to run setup. If `~/.local/bin` isn't on your `PATH`, add it.

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
2. **Model backend**: `claude-code`, `claude-api`, `local` or `codex`, and its sign-in; on `local`, your server and which of its models to run ([[Guides/Plugins and sign-ins#Model backend|Model backend]])
3. **Plan usage** (only if the app can't read your Claude plan's usage from Claude Code): reading it from your Claude Code status line instead ([[Guides/Settings#Usage limit|Usage limit]])
4. **Claude Code** (on the `claude-code` backend): setup connects Claude Code to the app's MCP server, without asking, so `/mcp` lists it in every folder ([[Guides/Using it from Claude|Using it from Claude]])
5. **Routines to fix**: any routine that can't run, and why
6. **Sign-ins**: each server your routines use. One that needs something set up first walks you through it before asking for anything ([[Guides/Plugins and sign-ins|Plugins and sign-ins]])
7. **Turn on routines**: what each routine can reach, for you to approve; one yes trusts it and runs it on its schedule ([[Guides/Routines#Turn it on|Turn it on]])

Run it in a terminal with `procauto setup`, which asks before each step and skips what's done, or open **Finish Setup…** from the menu bar or the Linux tray (on macOS also **Setup** in the Routines window), which shows every step with a button for each. Run it again any time, e.g. after adding a routine. Until the required steps are done, the menu bar dot is red with "Setup isn't finished".

## Update
`procauto update` installs the latest release if it's newer (`--check` only reports; `update progress` says what one installing is doing). It verifies the download like the installer, replaces the app where it is and restarts it. The menu bar app and the Linux tray offer it too (**Update to X…**, or **Check for Updates**), and show its progress. Your routines, settings and sign-ins are kept.

## Uninstall
`procauto uninstall` removes the app, its `procauto` link and its login items. Your config, routines, history, logs and secrets stay, unless you add `--purge`.

## Where things are
`procauto paths` shows where the app keeps its config file, routines folder, run status, history, notifications and templates. The data folder is `~/Library/Application Support/process-automator/` on macOS and `~/.config/process-automator/` on Linux; on macOS the scheduler logs to `~/Library/Logs/process-automator/scheduler.log`.

## On Linux
The CLI and the scheduler work as on macOS. Only templates that run on Linux are offered. The scheduler is the systemd user unit `procauto-scheduler.service` (logs: `journalctl --user -u procauto-scheduler`). systemd runs it only while you're logged in: on a server, to run routines when you're not, turn on lingering once (`sudo loginctl enable-linger $USER`; the installer says so when it's off). If you set `XDG_CONFIG_HOME`, set it for systemd's user session too (`~/.config/environment.d/`), or the scheduler reads another config folder than `procauto` (the installer notes it).

In a desktop session the **tray** takes the menu bar app's place: the same icon and status dot, menu and notifications ([[Guides/The menu bar app#The Linux tray|The Linux tray]]). It starts at login from `~/.config/autostart/procauto-tray.desktop`, and the installer starts it at once when you install from the desktop. On a machine with no desktop nothing starts it, and the app works the same without it.

On GNOME the tray shows only with the **AppIndicator extension**: Ubuntu enables it by default; elsewhere install "AppIndicator and KStatusNotifierItem Support" (package `gnome-shell-extension-appindicator`) and turn it on. KDE Plasma, Xfce, Cinnamon and most other desktops show it as they are.
