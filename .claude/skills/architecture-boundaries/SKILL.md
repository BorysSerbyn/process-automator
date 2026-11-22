---
name: architecture-boundaries
description: Keeps process-automator's layer boundaries intact (the UI depends only on the CLI; the core uses no plugins; plugins are OS-independent; the MCP server is the core's). Use when changing automator-ui/, automator-tray/ (and its windows/), core/, core/plugins/, the MCP server, contracts/ or the CLI's output, adding anything UI-visible, or touching OS-specific APIs (macOS, launchd, Keychain, systemd, /Applications, ~/Library).
---

# Architecture boundaries

**1. automator-ui (the indicator and its windows), automator-tray (Linux's tray) and automator-windows (Linux's windows) are only frontends, and depend on nothing but the CLI.** Each acts only by running `procauto` CLI commands and shows what they print as is (`health`: ok green, error red, none grey): run status through `status --watch` (one JSON line per change), the app's notifications through `notifications list --pending --json`. It reads none of the core's files and knows no path. Its one judgement: no status, or one past its `validUntil`, is red. No logic, and no knowledge of the core's internals: never compute health, schedules or times, hard-code setting keys (the core's output names them, e.g. a setup step's `setting`), or read config, routines or the Keychain. Need something new? Add it to Go (a CLI `--json` command or field), then display it.

**2. The Go core doesn't know who calls it.** The CLI is the API (contracts in `contracts/`, `make contracts`), and its only interface to frontends. No caller-specific branches, no UI wording or colours. All decisions live here.

**3. The Go core doesn't know which OS it runs on.** OS-specific things live outside shared code: external apps are MCP servers added as plugins, launchd/systemd/bundling in `Makefile` and `scripts/`, unavoidable OS calls behind a per-OS file with a fallback for other OSes (`secrets/default_darwin.go`, `filelock`, `cli/browser_darwin.go`). `internal/osboundary`'s test enforces it, and `core/` must cross-compile (`GOOS=linux go build ./...`, `GOOS=windows go build ./...`).

**4. The core uses no plugins.** Plugins live in `core/plugins/` (e.g. `notify`), each built only on `pluginapi` (nothing from `core/internal/`). The engine adds none: an app lists the ones it wants in its `automator.App.Plugins` (`cmd/procauto` lists `notify`), and the core runs that list for routines without knowing any by name: nothing in `core/internal/` or `core/automator` imports a plugin, no special cases. The app's own features never go through a plugin, and share nothing with one: its notifications (failed runs, sign-ins, updates) are the core's (`internal/notifications`, the `notifications` command, automator-ui's Notifier); the notify plugin's are the plugin's, with no package, type, file, setting or helper in common.

**5. A plugin is independent of the OS.** It works wherever the core does; what it needs from one OS sits in per-OS files inside the plugin (e.g. `notify`'s macOS and Linux delivery), with a clear answer elsewhere.

**6. The MCP server is part of the core, not a plugin.** `mcp serve` is the CLI's commands as MCP tools for Claude and other clients. There's no plugin for it and it isn't listed as one; routines don't use it. A plugin that needs the app talks to the server through `pluginapi`'s session to it.

**7. Only `core/automator`, `core/pluginapi` and `core/plugins/` are public.** Other apps import them (and build with this repo's Makefile and scripts), so keep them small and their changes deliberate; anything else goes in `internal/`. Nothing app-specific belongs in this repo: names come from the `automator.App` (`internal/product` in the core, `AppInfo` in automator-ui, the package's `install.env` in automator-tray), never literals.

When a `--json` shape changes, run `make contracts`: the Swift types are generated from `contracts/openapi.yaml` at build time, never hand-written.
