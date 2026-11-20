---
name: architecture-boundaries
description: Keeps process-automator's layer boundaries intact. Use when changing automator-ui/, core/, contracts/ or the run-status file, adding anything UI-visible, or touching OS-specific APIs (macOS, launchd, Keychain, systemd, /Applications, ~/Library).
---

# Architecture boundaries

**1. automator-ui (the indicator and its windows) is only a frontend.** It shows the run-status file as is (`health`: ok green, error red, none grey) and acts only by running `procauto` CLI commands. Its one judgement: a missing or expired (`validUntil`) status is red. No logic, and no knowledge of the core's internals: file locations come from `procauto paths --json`; never compute health, schedules or times, or read config, routines or the Keychain. Need something new? Add it to Go (run-status field or CLI `--json` command), then display it.

**2. The Go core doesn't know who calls it.** The CLI is the API (contracts in `contracts/`, `make contracts`); the run-status file is its only output to frontends. No caller-specific branches, no UI wording or colours. All decisions live here.

**3. The Go core doesn't know which OS it runs on.** OS-specific things live outside shared code: external apps are MCP servers added as plugins, launchd/systemd/bundling in `Makefile` and `scripts/`, unavoidable OS calls behind a per-OS file with a fallback for other OSes (`secrets/default_darwin.go`, `filelock`, `cli/browser_darwin.go`). `internal/osboundary`'s test enforces it, and `core/` must cross-compile (`GOOS=linux go build ./...`, `GOOS=windows go build ./...`).

When a run-status or `--json` shape changes, run `make contracts`: the Swift types are generated from `contracts/openapi.yaml` at build time, never hand-written.
