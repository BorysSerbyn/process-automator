# process-automator

Scheduled LLM routines: prompt files a model runs in the background, on a schedule, with tools from MCP servers.

- `core/internal/`: Go engine (`engine`: loading, readiness, runs, the scheduler, setup's steps, sign-ins; Genkit for LLM) + the `procauto` CLI over it (`cli`: commands, their output and questions), service-agnostic
- `core/plugins/`: plugins any app built on the engine may compile in (`notify`), each built only on `pluginapi` like any other; the engine adds none and nothing in `core/internal/` imports one. `cmd/procauto` lists `notify` in its plugins
- `core/cmd/procauto`: the binary, an `automator.App` handed to `automator.Main`. It compiles in `notify` (from `core/plugins`); routines use it and the MCP servers added with `procauto plugins add`. The app's MCP server (`procauto mcp serve`) is its interface for Claude and other MCP clients, not a plugin: routines don't get it
- The public surface is `core/automator` (an app: its names, identifiers, release source, plugins; `Main`, `CheckTemplates`) and `core/pluginapi` (the plugin contract); everything else stays in `internal/`. Other apps import them as a Go module, with their own plugins and templates, and build with this repo's Makefile and scripts. Keep both packages small and stable, and nothing app-specific in this repo
- Routines are Dotprompt files on the device, not in the binary; their settings are `automator.*` frontmatter keys. `templates/` holds shareable ones (none ship yet; an empty or missing folder must keep working)
- `automator-ui/`: the Swift macOS app: the indicator (menu bar icon, status dot and menu), the settings, routines and setup windows. It depends on nothing but the CLI (run status through `status --watch`, never the core's files). Its types are generated from `contracts/openapi.yaml` (`make contracts`)
- `automator-tray/`: the Linux tray, its own Go module (pure Go, CGO-free: `fyne.io/systray` and godbus): automator-ui's indicator, notifications and updates, through the CLI only, its names from the package's `install.env`. Windows open through one function (`tray.OpenWindow`): Setup and Settings in the windows app, the others the CLI in a terminal; its icons are made from the logo at packaging (`cmd/trayicons`)
- `automator-tray/windows/`: automator-windows, the Linux windows app (Fyne, cgo), a module of its own so the tray's dependencies and CGO-free builds don't change: automator-ui's Setup and Settings windows, one process per window, started by the tray. Built for releases in Docker (`scripts/build-linux-windows.sh`); tested on the Mac with Fyne's headless driver
- The core is OS-neutral: OS calls live in per-OS files (`name_darwin.go`, `name_unix.go`, `name_other.go`, or a `//go:build` line), OS-bound integrations are MCP servers, and delivering the app's own state to the OS (its notifications, the menu bar) is the frontends' job; the one exception, the notify plugin showing routines' notifications itself, has a darwin-only cgo file (`core/plugins/notify`). `internal/osboundary`'s test and `make ci`'s Linux/Windows cross-compiles enforce it
- Checks before a change is done: `make test` (Go with `-race`, the tray's and the windows app's too, Swift), `make lint`, `GOOS=linux go build ./...` and `GOOS=windows go build ./...` in `core/`; before pushing, `make ci` (there's no CI on GitHub: its workflows are disabled). Releases: `make release`, then `make publish` (docs/Guides/Developing.md)

## Docs

`docs/` is an Obsidian vault: `Home.md`, `Guides/` for users, and `Spec/` (scope, functional and technical requirements, technical decisions) describing the app as it is. See the `writing-docs` skill before editing it.

Requirements state unmoving facts about the app: never progress markers (no ✅, "implemented", "done", "not done yet", release or version tags). Reasons and rejected alternatives go in `Spec/Technical Decisions.md`.
