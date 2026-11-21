---
name: architecture-boundaries
description: Keeps process-automator's layer boundaries intact (the UI only runs the CLI; the core uses no plugins; plugins are OS-independent; the MCP server is the core's). Use when changing automator-ui/, core/, core/plugins/, the MCP server, contracts/ or the run-status file, adding anything UI-visible, or touching OS-specific APIs (macOS, launchd, Keychain, systemd, /Applications, ~/Library).
---

# Architecture boundaries

**1. automator-ui (the indicator and its windows) is only a frontend.** It shows the run-status file as is (`health`: ok green, error red, none grey) and acts only by running `procauto` CLI commands. Its one judgement: a missing or expired (`validUntil`) status is red. No logic, and no knowledge of the core's internals: file locations come from `procauto paths --json`; never compute health, schedules or times, or read config, routines or the Keychain. Need something new? Add it to Go (run-status field or CLI `--json` command), then display it.

**2. The Go core doesn't know who calls it.** The CLI is the API (contracts in `contracts/`, `make contracts`); the run-status file is its only output to frontends. No caller-specific branches, no UI wording or colours. All decisions live here.

**3. The Go core doesn't know which OS it runs on.** OS-specific things live outside shared code: external apps are MCP servers added as plugins, launchd/systemd/bundling in `Makefile` and `scripts/`, unavoidable OS calls behind a per-OS file with a fallback for other OSes (`secrets/default_darwin.go`, `filelock`, `cli/browser_darwin.go`). `internal/osboundary`'s test enforces it, and `core/` must cross-compile (`GOOS=linux go build ./...`, `GOOS=windows go build ./...`).

**4. The core uses no plugins.** Plugins live in `core/plugins/` (`notify` ships with every app built on the engine; apps add their own), each built only on `pluginapi`, and the core runs them for routines without knowing any by name: no imports of a plugin, no special cases. The app's own features never go through a plugin: its notifications (failed runs, sign-ins, updates) are the core's, delivered by automator-ui; a plugin's notifications are the plugin's.

**5. A plugin is independent of the OS.** It works wherever the core does; what it needs from one OS sits in per-OS files inside the plugin (e.g. `notify`'s macOS and Linux delivery), with a clear answer elsewhere.

**6. The MCP server is part of the core, not a plugin.** `mcp serve` is the CLI's commands as MCP tools for Claude and other clients. There's no plugin for it and it isn't listed as one; routines don't use it. A plugin that needs the app talks to the server through `pluginapi`'s session to it.

**7. Only `core/automator`, `core/pluginapi` and `core/plugins/` are public.** Other apps import them (and build with this repo's Makefile and scripts), so keep them small and their changes deliberate; anything else goes in `internal/`. Nothing app-specific belongs in this repo: names come from the `automator.App` (`internal/product` in the core, `AppInfo` in automator-ui), never literals.

When a run-status or `--json` shape changes, run `make contracts`: the Swift types are generated from `contracts/openapi.yaml` at build time, never hand-written.
