# Contracts

The interfaces between the Go core and anything that talks to it (the menu bar app, future settings UI, scripts).

| File | What | Source |
|---|---|---|
| `cli.yaml` | Every CLI command: usage, flags, which JSON schema its `--json` output follows | Generated |
| `cli/<command>.schema.json` | JSON Schema of each command's `--json` output | Generated |
| `settings.schema.json` | Every setting: type, default, limits, unit, description | Generated from the settings registry |
| `run-status.schema.json` | The run-status file the core writes and frontends watch | Generated from the run-status types |
| `openapi.yaml` | Every type a frontend reads (run status, each `--json` output) as OpenAPI components | Generated |
| `examples/` | Example files both the Go and Swift test suites check against | Hand-written |

Generated files come from `core/` (`make contracts`) and are committed; Go doc comments become schema descriptions. A Go test fails when they're out of date, so the code and the contract can't drift.

Frontends generate their types from `openapi.yaml` instead of hand-writing them: automator-ui does it at build time (Apple's swift-openapi-generator plugin, via a link to this file in `automator-ui/Sources/AutomatorUICore/`). Change a Go type, run `make contracts`, and its types follow.

Adding a setting: add one entry to `core/internal/settings/registry.go`, then `make contracts`.
