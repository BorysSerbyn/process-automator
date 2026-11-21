Back to [[Home]]. Driven by the [[Spec/Requirements/Technical/Technical Requirements|Technical Requirements]] and [[Spec/Requirements/Technical/Distribution|Technical: Distribution]].

## Decision: Go
The core is Go: a scheduled CLI doing mostly HTTP and JSON, shipped as a single static binary with simple tooling. It has the official Anthropic SDK (`anthropic-sdk-go`) and the official MCP SDK (`modelcontextprotocol/go-sdk`, co-maintained with Google).

### Rejected
- **Rust**: no official Anthropic SDK and a younger MCP SDK (`rmcp`); its performance and memory advantages don't matter here

## Decision: Genkit (Go) as the AI framework
[Genkit](https://github.com/firebase/genkit) handles model calls, tool calling and prompts: a first-party Anthropic plugin built on `anthropic-sdk-go`, [Dotprompt](https://google.github.io/dotprompt/) built in (one file per prompt, YAML frontmatter plus a template), tracing that shows the exact prompt and tokens of a run, and one API across models. The accepted cost: a framework at the core, so leaving it would be a rewrite, and Claude-specific API features arrive only once its plugin supports them.

### Rejected
- **LangGraph**: Python/JS only; its graph orchestration solves branching, stateful agents, while routines are linear (gather, decide, act)
- **An existing agent runtime** (e.g. Hermes Agent): scheduling and MCP built in, but a Python runtime with its own always-on gateway, less control over the write path (guardrails in prompts rather than code), and a young dependency at the core. Revisit if routines ever outgrow fixed prompts

## Decision: two model backends: an API key, or a headless agent CLI on my plan
The model loop sits behind a `Backend` interface chosen by `llm.backend`. `api` runs it in Genkit against the Anthropic API, paid per token. `claude-code` runs headless Claude Code on my Claude plan: a plan's credentials are reserved for Anthropic's own apps, and scripting Claude Code is the sanctioned way to use a plan from another program. `claude-code` is the first of a family: another agent CLI (Codex, Gemini CLI, …) is one `CLITool` plus a backend name, and gets prompt rendering, the loopback tool server, dry runs and the working folder for free. Claude Code is isolated from my own setup (no settings, hooks, plugins, slash commands, sessions or memory, and no inherited API key) so a run does only what its routine says and can't silently bill an API key. The accepted cost on `claude-code`: only `model` applies from the frontmatter, the loop is Claude Code's (I control max turns), and only Claude models run.

### Rejected
- **Passing plan credentials to Genkit or the API**: against Anthropic's terms, and blocked
- **API only**: everyone with a Claude plan would pay twice

## Decision: an in-process scheduler in a long-running daemon
`procauto daemon` runs the scheduler; the OS (a LaunchAgent with `KeepAlive`, or a systemd user unit) only starts it at login and keeps it alive. It polls every 30s rather than sleeping until the next slot, which keeps it correct across sleep and wake, and the same daemon runs on both OSes. Started by the OS with a minimal PATH, it adds my login shell's PATH (as Goose's desktop app does), so `claude` and MCP servers are found as in a terminal.

### Rejected
- **OS scheduling** (launchd `StartCalendarInterval`, systemd timers): a job per routine rewritten on every settings change, per OS, and unable to report "missed" or "can't be scheduled"; cron schedules, staleness and catch-up are the app's policy

## Decision: every failure fails the run
"Success" means the routine did its job, not that the run didn't crash, so the outcome takes the strongest of: a backend error, a sign-in failing, a write whose last attempt failed, the routine's own `report_outcome` (where no report is a failure). Errors bubble up, with one exception: a tool error inside the model loop goes back to the model, which can often recover (retry, search differently); an unrecovered one still fails the run. Anything the scheduler can't do is recorded as a run or a problem, never only logged.

### Rejected
- **Trusting the model's final text**: a model can say it posted when the post was refused
- **Treating an unreadable source as empty**: "no data" and "couldn't read" would look the same

## Decision: a blank-slate engine; integrations are MCP servers
The engine knows no specific service, and the app ships with no integrations: a plugin is an MCP server registered in config, written in any language, so routines reach every service the same way and any server built for other MCP clients works as is. The core keeps only what it owns: `automator` (its own read-only tools) and `notify` (it writes the core's notifications). The in-process plugin API stays, for Go plugins compiled into a build.

### Rejected
- **Bundled presets for particular services**: each needs upkeep and ties the app to services I may not use; `plugins add` covers them with no code
- **Go's `plugin` package**: Linux and macOS only, needs exact toolchain and dependency matches with the host binary
- **HashiCorp go-plugin**: a custom RPC ABI heavier than MCP, and plugins would only work with this app

## Decision: other apps import the engine as a Go module, through a small public surface
An app with its own plugins, templates and names is a separate repository whose `go.mod` requires `core/` at a pinned commit and whose main package calls `automator.Main` with an `automator.App`. Only `core/automator` and `core/pluginapi` are public, so the engine stays free to change everything else. It compiles into one binary with nothing downloaded at run time, and the app's repository can stay private on its own. The build is shared too: the app runs this repository's Makefile and scripts from a checkout at the pinned version, with its own values, and the one automator-ui reads the app's name and CLI from its bundle.

The identity is said three times (the `App`, `install.sh` and the Makefile) because each is read where the others can't be: the binary at run time, the installer before anything is installed, the build before the binary exists. A test keeps them in step. What changes from build to build (the version, the checkout, the packaging layout, a bundle prefix of its own for CI) is set at link time in `core/automator`, so the link-time names stay inside this repository.

### Rejected
- **A fork per app**: every engine fix merged twice, and the copies drift
- **Plugins and templates fetched at run time**: a second distribution channel to secure, and the app would no longer be one verified artifact
- **Link-time values set by the app in internal packages** (`-X …/internal/…`): a renamed variable is silently ignored by the linker, across two repositories
- **Templates compiled into the binary**: `paths`, `uninstall` and frontends use the templates folder, and a shipped folder keeps templates readable and editable in a checkout

## Decision: MCP connections use the official Go SDK directly
MCP sessions are opened with `modelcontextprotocol/go-sdk`; Genkit gets thin tool wrappers over them, limited to each routine's allowlist. This gives full control over the unattended path (a run never opens a browser; an expired or revoked credential is a distinct `needs-reauth`), and the allowlist and dry-run interception happen in my code.

### Rejected
- **Genkit's MCP plugin**: allowlisting and credential handling would happen inside a plugin I don't control

## Decision: concurrency via a per-credential refresh lock, not a run lock
Routines may run in parallel. The hazard that forced one run at a time is OAuth refresh-token rotation, so that's what gets locked: one refresh per credential at a time, across processes, re-reading the stored token after taking the lock. Routines that wait their turn still take a queue lock.

### Rejected
- **Parallel only for routines without OAuth servers**: would rule out most routines, and adding a server could silently change whether a routine may run in parallel

## Decision: routines are Dotprompt files on the device
Each routine is one Dotprompt file in the routines folder; the binary contains none. Routine settings are Dotprompt extension fields (`automator.*`), the format's own mechanism for custom frontmatter, so they can't collide with Dotprompt keys, and the folder follows Dotprompt's conventions (`_name.prompt` is a partial). Routines change without app updates, and where to act and how to format belongs in the prompt, not code.

### Rejected
- **Routines in Go code**: every change would be a release
- **A manifest listing routines**: a second file to keep in step; the folder is the list

## Decision: a routine has one schedule, in its file
A routine's schedule is one of its settings: in its file, overridable in settings. Running the same thing on two schedules, or with different parameters, means two routines (e.g. one template installed twice). That keeps one routine as one thing in the menu, with one health and one history.

### Rejected
- **Schedules as separate entries pointing at routines** (as Goose does with recipes): one routine would have several healths and histories

## Decision: templates are shared routine files; none ship
A **template** is a routine file tracked in the repository's `templates/` so anyone can install it as is or adapt it, project-specific ones included; installed, it's an ordinary routine. "Routine" is what runs and "template" what's shared, so one thing never has two names. The app ships none of its own: templates are the user's or their team's to add, and the feature works with an empty folder.

### Rejected
- **"Recipe"** (Goose's name): a second name for a routine
- **Compiling templates into the binary**: sharing one would need a release

## Decision: frontends talk to the core through the CLI and the run-status file
The CLI is the API: a frontend runs commands with `--json` to read state and make changes, and watches the run-status file the core writes (file events, with a slow poll as fallback). The core never needs to know a frontend exists, either side restarts independently, and the same works on any OS. Frontends hold no logic: the core decides health, and the one thing a frontend judges is whether the core is responding (the scheduler's heartbeat).

### Rejected
- **Push over a local socket or XPC**: a protocol and a connection lifecycle to maintain for no gain at this scale; reconsider if a frontend ever needs live streaming of a run

## Decision: frontend types are generated from the Go types
The Go types are the source of truth: `make contracts` emits JSON Schemas and `contracts/openapi.yaml` (Go doc comments as descriptions), and automator-ui generates its Swift types from it at build time (Apple's swift-openapi-generator), as Goose generates its UI's client from its Rust types. The core and the UI ship together, so decoding the full types is safe.

### Rejected
- **Hand-written Swift types**: they'd drift from the core unnoticed

## Decision: automator-ui is a separate Swift app, macOS only
The UI is a native SwiftUI/AppKit app, separate from the Go core, which stays pure Go. Every cross-platform option loses on macOS what the Swift app does well (dependable notification clicks, time-sensitive notifications, native menu bar behaviour, about 11 MB idle), and Linux trays need extensions on stock GNOME whatever the toolkit, so Linux runs the CLI and scheduler only. The app is named for being a UI app (automator-ui); the indicator is its menu bar part.

### Rejected
- **A Go menu bar library** (e.g. `fyne.io/systray`): one binary, but reaches AppKit through cgo
- **Tauri v2**: a new language; on Linux, clicking the tray icon fires no events; no time-sensitive notifications; a web runtime's memory
- **Wails v3**: the core's language, but in beta, with an unstable tray API
- **Qt 6, Fyne, Flutter, Electron**: a heavy toolchain, non-native windows without notification clicks, uneven Linux plugins, or far too heavy
- **Native shells per OS with one shared web UI**: only pays off if a Linux menu bar app is wanted; the UI having no logic keeps a second shell cheap if it ever is

## Decision: Claude manages routines through the core's own MCP server
People make routines by talking to Claude, so Claude is another frontend: `procauto mcp serve` serves the same surface as the CLI. Every CLI command is a tool by default, derived from the command tree, and one override table holds the exceptions, so the server isn't edited when the CLI grows. The guardrail is my approval rather than missing tools: side-effecting tools are annotated so the client asks first, created routines start disabled and untrusted, and trust stays in my terminal. Routines get only the read-only tools.

### Rejected
- **Claude uses the CLI through a shell**: works in Claude Code, but not in clients without a shell, not inside routines, and the model would parse help text instead of typed tools
- **Withholding real runs and enabling from MCP**: safer by default, but breaks parity; per-call approval gives the same control

## Decision: setup connects Claude Code to the app's MCP server, without asking
On the `claude-code` backend I already live in Claude Code, so its `/mcp` should list the app from the start: an install that only offers it gets skipped. So it's a required setup step, done without asking (`auto`): it only adds a server to Claude Code, whose own approval still guards every call that changes anything. It's registered at user scope, so it's there in every folder, and by the `~/.local/bin` link rather than the binary inside the app, so an update doesn't break it. Whether it's connected is read from Claude Code's config file: the scheduler checks setup on every pass, and that must stay quick and can't depend on Claude Code starting.

### Rejected
- **Offering it at the end of the install**: optional steps after everything works get skipped, and then Claude doesn't know the app
- **`claude mcp get` to check it**: starts Claude Code on every scheduler pass, slow and flaky when it updates itself or isn't signed in
- **Asking first**: a question with only one sensible answer; the change is to Claude Code's list of servers, and undone with `claude mcp remove`

## Decision: turning a routine on is one step: trust and resume
New routines start paused and untrusted, and a routine is only useful once both are undone, so setup asks once per routine: what it can reach, then one yes that trusts it and resumes it (`routines trust --resume`). Trust and pause stay separate states, and separate commands: trusting doesn't resume, so a routine paused on purpose stays paused when its trust is renewed after a change, and pausing keeps its trust.

### Rejected
- **A trust step, then a schedule step**: two questions about the same routine, the second asked only after the first was done, for one decision
- **Trusting always resumes**: re-trusting a paused routine after a change would turn it back on behind my back
- **A new command for both**: one more name for a review that's the trust command's; a flag on it keeps one review and one prompt

## Decision: plugins reach the app's own data through the `automator` server
The core serves its routines and runs (including saved session IDs) to plugins and routines as the in-process `automator` server. Plugins keep a single way in (`Host.Connect`) and never read the core's files.

### Rejected
- **Plugins reading the data folder**: ties every plugin to the core's file layout

## Decision: MCP sign-in runs across calls
A sign-in can need several pages, and from protocol 2026-07-28 a tool can't ask the client anything mid-call: it returns the page as an input request and the client calls again. So `auth login` runs the sign-in in the background, keyed by the call's request state, and each call returns the next page or the outcome. For older clients the SDK makes those round trips itself, so one implementation serves both. Secrets go through a page in my browser, never through the model.

### Rejected
- **Eliciting mid-call** (`ServerSession.Elicit`): the SDK refuses it on current protocol versions, so sign-in would only ever fall back to opening a browser on this machine

## Decision: dry runs withhold write-capable built-in tools
Agent CLI built-in tools (Bash, Edit, Write, …) run inside the agent CLI, where the tool gateway can't see or intercept their calls. So a dry run gets only the read-only built-ins (Read, Glob, Grep, WebSearch, WebFetch), and the others are left out entirely.

### Rejected
- **Intercepting them like MCP writes**: their calls never pass through the gateway

## Decision: the core adds a routine folder's CLAUDE.md itself
Claude Code loads a folder's `CLAUDE.md` only through its `project` setting source, which also loads that folder's settings and hooks, and only in folders the user has trusted. So runs keep every setting source off, and the core reads `<folder>/CLAUDE.md` and adds it to the system prompt: the same instructions, with no hooks or settings, in any folder.

### Rejected
- **`--setting-sources project`**: a folder's hooks would run unattended commands, and its settings could widen permissions

## Decision: judgement over rules for routine output
Output quality is left to the model: prompts stay lean, and recurring mistakes are fixed in the routine file or with memories. Checks in code would have to be declared per routine, and routines are just files.

### Rejected
- **Code-side output validation**: needs a way for routine files to declare checks, or breaks "routines are just files"; revisit if memories stop being enough
- **A human review step before writes**: defeats running unattended; dry runs cover previewing

## Decision: memories are plain files, written only by me
Memories live in plain Markdown files in the data folder, one per scope: diffable and hand-editable. Routines read them and never write them, and the backend's own memory is off, so what a routine is told to remember is always something I said.

### Rejected
- **A database**: not hand-editable, for a few lines of text
- **Letting the agent add memories**: rules would accumulate that I never chose

## Decision: the usage limit follows the backend: plan usage for Claude, dollars for an API key
Cost means different things on the two backends. On a Claude plan, runs are covered until its 5-hour or weekly limit, so the limit is a share of the plan's usage; with an API key every token is paid, so the limit is a budget in dollars. The app never works out a cost for plan runs: Claude Code's figure is an estimate at API prices, not a charge. The plan's usage comes from Claude Code's headless `/usage`, structured and using no tokens; it's marked experimental, so it's read defensively, and if it stops being readable the app falls back to Claude Code's status line, which also carries the plan's usage: setup wraps my status line command with the app's, which records the usage and runs mine, so nothing changes for me. An unreadable `/usage` is setup to finish, not a health problem.

### Rejected
- **API-list-price dollars for both backends**: a Claude plan isn't charged per token, so a dollar budget there limits the wrong thing
- **A budget for paid extra usage on a Claude plan**: nothing documented says whether an account has it or what it has spent
- **A usage limit for the API key**: its limits are set in the Anthropic Console and can't be read without an admin key
- **Claude Code's internal usage endpoint**: undocumented, and the app would have to read Claude Code's credentials
- **The status line as the main source**: it only gets readings while I use Claude Code interactively
- **Asking me to edit my status line script**: the app can wrap the command itself
- **The rate-limit event in a run's output**: its percentages are in an undocumented field, and it only updates when a routine runs

## Decision: secrets through the OS's own command-line tools
The Keychain is reached by shelling out to `/usr/bin/security` and the Secret Service by `secret-tool` (libsecret), values on stdin and base64-encoded, behind one `secrets.Store` interface. No cgo and no D-Bus library in the core; `secret-tool` is a package away on every desktop distribution.

### Rejected
- **cgo Keychain bindings**: cgo in an otherwise pure-Go core
- **A Go Secret Service client** (godbus, go-keyring): a new dependency tree for four calls
- **An encrypted file**: its key would sit next to it, or need a password at every run

## Decision: one installer script, inside every artifact
Installing is one bash script, `install-app.sh`, shipped inside the artifact it installs, with an `install.env` the build writes. `make install`, `install.sh`, `procauto update`, `procauto uninstall` and the Homebrew cask all run it, so a fix to installing reaches every path, and each version installs itself with its own rules (an update runs the new version's script, after verifying it). launchd, systemd and the secrets stores stay in the script and the per-OS files, out of the core.

### Rejected
- **Installing in Go** (`procauto install`): OS-specific installing in the core, against its OS boundary; and the first install would need a binary to install the binary
- **Separate logic per path** (Makefile, `install.sh`, `update`, Homebrew): four copies to keep in step
- **A `.pkg` or `.dmg`**: needs admin rights or a drag to Applications, and still wouldn't set up the CLI and login items

## Decision: releases are verified by checksum and the Developer ID, not by a key of my own
An artifact is checked against its release's `SHA256SUMS` on every platform; on macOS the app must also verify as signed and, once the build knows its `TEAM_ID`, carry that team's notarized Developer ID signature. The team ID is pinned in `install.sh` and compiled into the app, so a release swapped by someone who can write to the repository but doesn't hold the Apple certificate is refused. On Linux the checksum guards the download only.

### Rejected
- **Signing `SHA256SUMS` with my own key** (minisign, ed25519): a private key to keep in CI and rotate, and a verifier on every machine, for a few people who already trust the repository; reconsider if the app opens up
- **GitHub artifact attestations**: need `gh` on every machine, and GitHub Enterprise Cloud for a private repository
- **Checksum only on macOS too**: the Developer ID is already required for Gatekeeper, and checking it is free

## Decision: macOS releases are for Apple silicon only
A release builds the macOS app for Apple silicon only: building automator-ui for Intel too doubled the release build, for Macs nobody using the app has. The installer and `update` say so on an Intel Mac instead of installing an app that can't run.

### Rejected
- **A universal app**: twice the build time, for no one

## Decision: releases can be self-signed
Without an Apple Developer account, releases are signed with a self-signed certificate kept in CI, the same for every release. Downloaded by `install.sh` or `update`, the app carries no quarantine, so Gatekeeper doesn't check it; and since macOS ties grants to the certificate, they survive updates. With `TEAM_ID` empty, installs accept any valid signature, so the checksum is the only check and a release swapped by someone who can write to the repository isn't refused. Setting up a Developer ID later is adding its secrets and `TEAM_ID`.

### Rejected
- **Ad-hoc signing**: a new identity every release, so every grant and Keychain item would be asked for again after each update
- **Waiting for a Developer ID**: no release anyone else can install until then

## Decision: releases are read with the GitHub CLI's sign-in
The repository is private, so reading releases needs a GitHub token. `update` and `install.sh` use `GH_TOKEN`/`GITHUB_TOKEN` when set, else the GitHub CLI's sign-in, and no token otherwise. Anyone invited already signs in to GitHub to get the code or the tap, so there's no new credential, and the app never stores one.

### Rejected
- **A `github` sign-in in the secrets store**: one more secret to hold, ask for and refresh, for access people already have through `gh`
- **Public release assets from a private repository**: GitHub doesn't offer it; a separate public releases repository would share the app beyond invited people

## Decision: `update` replaces the app where it's installed
An update installs over the running app's own location, whatever installed it, so updating from automator-ui works for an app installed by `install.sh`, `make install` or the Homebrew cask (which declares `auto_updates`, so brew leaves it to the app). Only the Homebrew formula's install, in brew's Cellar, is updated with `brew upgrade`.

### Rejected
- **Always `brew upgrade` for Homebrew installs**: automator-ui would need brew and its token in its environment, and the cask's app can't be told apart from one installed by hand
- **Always building from a source checkout**: needs Go, Xcode and a checkout on every machine, and signs with a local certificate; it remains only the fallback while no release has a download for the machine

## Decision: development builds keep one signing identity
Every local build is signed with the same self-signed certificate, with a stable bundle ID and install path, and reinstalling restarts the apps in place. macOS ties permission grants and Keychain access to the signing identity, so rebuilding never asks for them again.

### Rejected
- **Ad-hoc signing**: a new identity every build, so every grant and Keychain item would be asked for again
