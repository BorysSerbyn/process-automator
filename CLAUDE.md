# process-automator

Scheduled LLM routines: prompt files a model runs in the background, on a schedule, with tools from MCP servers.

- `core/internal/`: Go engine (`engine`: loading, readiness, runs, the scheduler; Genkit for LLM) + the `procauto` CLI over it (`cli`: commands and their output), service-agnostic
- `core/cmd/procauto`: the binary. It compiles in no plugins: routines use the core's built-in servers (`automator`, the app's read-only tools; `notify`, `notify_me`) and the MCP servers added with `procauto plugins add` (`internal/pluginapi` is the plugin contract)
- Routines are Dotprompt files on the device, not in the binary; their settings are `automator.*` frontmatter keys. `templates/` holds shareable ones (none ship yet; an empty or missing folder must keep working)
- `automator-ui/`: the Swift macOS app: the indicator (menu bar icon, status dot and menu), the settings, routines and setup windows. Its types are generated from `contracts/openapi.yaml` (`make contracts`)
- The core is OS-neutral: OS calls live in per-OS files (`name_darwin.go`, `name_unix.go`, `name_other.go`, or a `//go:build` line), OS-bound integrations are MCP servers, and delivering to the OS (notifications, the menu bar) is the frontends' job. `internal/osboundary`'s test and CI's Linux/Windows cross-compiles enforce it
- Checks before a change is done: `make test` (Go with `-race`, Swift), `make lint`, `GOOS=linux go build ./...` and `GOOS=windows go build ./...` in `core/`

## Docs

`docs/` is an Obsidian vault: `Home.md`, `Guides/` for users, and `Spec/` (scope, functional and technical requirements, technical decisions) describing the app as it is. See the `writing-docs` skill before editing it.

Requirements state unmoving facts about the app: never progress markers (no ✅, "implemented", "done", "not done yet", release or version tags). Reasons and rejected alternatives go in `Spec/Technical Decisions.md`.
