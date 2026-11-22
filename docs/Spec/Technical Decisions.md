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

## Decision: model backends: an API key, or a headless agent CLI on my plan
The model loop sits behind a `Backend` interface chosen by `llm.backend`. `claude-api` runs it in Genkit against the Anthropic API, paid per token. `claude-code` runs headless Claude Code on my Claude plan: a plan's credentials are reserved for Anthropic's own apps, and scripting Claude Code is the sanctioned way to use a plan from another program. Agent CLIs are a family: each (Claude Code, Codex; Gemini CLI or another later) is one `CLITool` plus a backend name, and gets prompt rendering, the loopback tool server, dry runs and the working folder for free. Claude Code is isolated from my own setup (no settings, hooks, plugins, slash commands, sessions or memory, and no inherited API key) so a run does only what its routine says and can't silently bill an API key. The accepted cost on `claude-code`: only `model` applies from the frontmatter, the loop is Claude Code's (I control max turns), and only Claude models run.

### Rejected
- **Passing plan credentials to Genkit or the API**: against Anthropic's terms, and blocked
- **API only**: everyone with a Claude plan would pay twice

## Decision: an in-process scheduler in a long-running daemon
`procauto daemon` runs the scheduler; the OS (a LaunchAgent with `KeepAlive`, or a systemd user unit) only starts it at login and keeps it alive. It polls every 30s rather than sleeping until the next slot, which keeps it correct across sleep and wake, and the same daemon runs on both OSes. Started by the OS with a minimal PATH, it adds my login shell's PATH (as Goose's desktop app does), so `claude` and MCP servers are found as in a terminal. Stopping it signals the daemon alone (launchd does; systemd is told to, `KillMode=mixed`): it cancels its runs and records them as interrupted, where a run's own process signalled beside it could end first and be recorded as a failure.

### Rejected
- **OS scheduling** (launchd `StartCalendarInterval`, systemd timers): a job per routine rewritten on every settings change, per OS, and unable to report "missed" or "can't be scheduled"; cron schedules, staleness and catch-up are the app's policy

## Decision: every failure fails the run
"Success" means the routine did its job, not that the run didn't crash, so the outcome takes the strongest of: a backend error, a sign-in failing, a write whose last attempt failed, the routine's own `report_outcome` (where no report is a failure). Errors bubble up, with one exception: a tool error inside the model loop goes back to the model, which can often recover (retry, search differently); an unrecovered one still fails the run. Anything the scheduler can't do is recorded as a run or a problem, never only logged.

### Rejected
- **Trusting the model's final text**: a model can say it posted when the post was refused
- **Treating an unreadable source as empty**: "no data" and "couldn't read" would look the same

## Decision: a blank-slate engine; integrations are MCP servers
The engine knows no specific service, and the app ships with no integrations: a plugin is an MCP server registered in config, written in any language, so routines reach every service the same way and any server built for other MCP clients works as is. `core/plugins` holds plugins any app may compile in, such as `notify` (desktop notifications from routines); the engine adds none, and an app lists the ones it wants. The in-process plugin API stays, for Go plugins compiled into a build.

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

## Decision: pluginapi holds only what a plugin is written against
`pluginapi` has what an app's author writes: a `Plugin` that is either a preset external server (`External`) or Go code (`InProcess`, the fields that only Go code has), the `Host` the engine gives Go code, the run it serves, the server spec, two helpers for calling another server's tools, and `plugintest` for tests. Validating specs and plugins, expanding secret references and naming secrets in the store are the engine's (`internal/config`, `internal/engine`), so they change without breaking an app, and an app's plugins are checked once, when `automator.Main` starts. The engine's own record of a run (with every plugin's routine section) is internal too: a plugin only ever sees its own section.

### Rejected
- **One struct with every field, checked by hand**: which fields apply to which kind was only in `Validate`, and every caller asked `Serve != nil` again
- **Validation and secret expansion as methods on the public types**: every change to them would have been a breaking change for every app
- **A test host in each app**: each app wrote the same fake `Host`, and could drift from how the engine decodes sections

## Decision: in-process plugins' secrets are their own; external servers' are shared keys
An in-process plugin declares its secrets by name, with a prompt, and they're stored under `plugin/<name>/<secret>`: only that plugin reads them (`Host.Secret` refuses others), and `auth logout <name>` forgets them all. An external server references keys in its spec (`{key}` in `env` or `headers`), which are global: two servers for the same service share a key, and `auth logout` keeps a key another server still references. Both are signed in to with `auth login <name>`, from one list of what each name needs (the model backends' keys too), which `auth logout` and `auth status`, setup and readiness read as well.

### Rejected
- **Namespacing external servers' keys too**: a key referenced by two servers would be asked for twice, and specs written for other MCP clients name keys freely
- **Global keys for in-process plugins**: a plugin could read any secret by guessing its key; declared, namespaced ones keep it to its own

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

## Decision: the notify plugin shows its notifications itself; the app's own go through the frontends
`notify` is a plugin routines choose to use, so it delivers what it's asked to the OS on its own: on macOS through UserNotifications, as the app (the core lives in the app's bundle and is signed with its bundle ID, so it shares the app's permission to notify), on Linux through the freedesktop notification service. It needs no frontend, no file and no settings. Clicking one does nothing: a plugin only runs during its routine's run, and on macOS a click goes to the app's main executable, automator-ui, which knows nothing of it. The app's own notifications (a run that failed, needs a sign-in, was skipped or missed; an update) are the core's: recorded in its notifications file, delivered by automator-ui on macOS and the tray on Linux, and clicking one opens the app (on the run it's about, in its routine's window); their only setting is an on/off for the run ones (`runs.failure-notifications`). Nothing a routine does reaches them.

### Rejected
- **Routines' notifications through the app's file and automator-ui**: ties a plugin to the menu bar app, so nothing shows without it (on Linux, nothing at all), and its settings (what a click does) ended up as the app's
- **An app-wide click action with per-routine overrides**: it governed the app's notifications too, so a routine's setting changed what clicking "<routine> failed" did
- **The app notifying through `notify_me`**: the app's alerts would depend on a plugin, its trust and dry runs
- **Shelling out to `osascript` or `notify-send`**: notifications attributed to Script Editor rather than the app, or a tool that may not be installed
- **A click on the app's own opening a readable report of the run in another program**: leaves the app for a text file, when the routine's window shows the run with everything else about it

## Decision: cgo for macOS notifications, and only there
The core is pure Go, except one darwin-only cgo file in the notify plugin: UserNotifications has no command-line or pure-Go way in that keeps the notification the app's. It builds only with `darwin && cgo` (without cgo, macOS says notifications aren't supported), so Linux and Windows builds and cross-compiles stay CGO-free, and Linux uses D-Bus in pure Go.

### Rejected
- **A helper in automator-ui** (the core asks the Swift app to post): the plugin would depend on the menu bar app running, the coupling this removes
- **A separate small Swift or Objective-C helper binary**: another executable to build, sign and ship for one call

## Decision: frontends talk to the core through the CLI only
The CLI is the API: a frontend runs commands with `--json` to read state and make changes, and follows run status with `status --watch`, a long-running command that prints a JSON line at once and on each change. It reads none of the core's files, so the files' places and shapes stay the core's to change, and a frontend needs nothing but the CLI. The core never needs to know a frontend exists, either side restarts independently, and the same works on any OS. Frontends hold no logic: the core decides health, and the one thing a frontend judges is whether the core is responding (the scheduler's heartbeat, `validUntil`).

`status --watch` checks the file's time and size once a second: a stat, next to nothing, with no file-watching dependency, and the dot follows within a second.

### Rejected
- **Frontends reading the run-status and notifications files** (as they used to, with a folder watch): a second interface beside the CLI, tying the UI to the core's file layout
- **Push over a local socket or XPC**: a protocol and a connection lifecycle to maintain for no gain at this scale; reconsider if a frontend ever needs live streaming of a run
- **Polling `status --json` from the UI**: a process every few seconds, and a slower dot

## Decision: frontend types are generated from the Go types
The Go types are the source of truth: `make contracts` emits JSON Schemas and `contracts/openapi.yaml` (Go doc comments as descriptions), and automator-ui generates its Swift types from it at build time (Apple's swift-openapi-generator), as Goose generates its UI's client from its Rust types. The core and the UI ship together, so decoding the full types is safe.

### Rejected
- **Hand-written Swift types**: they'd drift from the core unnoticed

## Decision: automator-ui is a separate Swift app, macOS only
The UI is a native SwiftUI/AppKit app, separate from the Go core, which stays pure Go (but for one cgo file, see the notify plugin). Every cross-platform option loses on macOS what the Swift app does well (dependable notification clicks, time-sensitive notifications, native menu bar behaviour, about 11 MB idle), so Linux has a frontend of its own (the tray, below). The app is named for being a UI app (automator-ui); the indicator is its menu bar part.

### Rejected
- **A Go menu bar library** (e.g. `fyne.io/systray`): one binary, but reaches AppKit through cgo
- **Tauri v2**: a new language; on Linux, clicking the tray icon fires no events; no time-sensitive notifications; a web runtime's memory
- **Wails v3**: the core's language, but in beta, with an unstable tray API
- **Qt 6, Fyne, Flutter, Electron**: a heavy toolchain, non-native windows without notification clicks, uneven Linux plugins, or far too heavy
- **Native shells per OS with one shared web UI**: only pays off if a Linux menu bar app is wanted; the UI having no logic keeps a second shell cheap if it ever is

## Decision: Linux's tray is a small pure-Go process; its windows a separate one, on demand
The Linux frontend is automator-tray, a Go binary on `fyne.io/systray`, which speaks StatusNotifierItem and dbusmenu straight over D-Bus (godbus): no cgo, GTK or libappindicator, so it builds CGO-free like the core, ships as one small static binary beside it, and stays about 10 MB resident, all day. Notifications go to `org.freedesktop.Notifications` the same way, with a default action for clicks and buttons for asking. It's a frontend like automator-ui (the CLI only, no names of its own), in its own Go module so the core's dependencies don't grow. Its icon frames are PNGs made from the app's logo when it's packaged, so the running tray only loads files; busy is two frames shown in turn rather than an animation, a signal every 0.8 seconds and only while a routine runs. Windows are a separate process (automator-windows, below), started when one is opened and gone when it's closed, so the toolkit is never resident. On stock GNOME a StatusNotifierItem shows only with the AppIndicator extension, which Ubuntu enables by default; any toolkit's tray has the same need.

### Rejected
- **GTK in the tray process** (libayatana-appindicator through cgo): cgo in the Linux build, and a GTK stack resident all day for an icon and a menu
- **WebKit-based** (Tauri, Electron, Wails): a web engine resident for an icon and a menu, tens to hundreds of MB; Tauri's Linux tray fires no click events
- **One process for the tray and the windows** (Fyne's own tray): its OpenGL window stack, through cgo, would stay loaded while no window is open, which is nearly always
- **Notifications through `notify-send`**: may not be installed, and gives no clicks back

Where the notification service has no buttons, a window asks before an update installs, showing what `update --check` says is available (the tray decides nothing); without the windows app, the terminal asks (`update --ask`, the core saying what's available and asking). Whatever the tray waits for (a CLI command, the notification service) has a time limit: one that hangs mustn't stop notifications or update checks for the rest of its life.

## Decision: Linux's windows are Fyne, one process per window, in a module of their own
automator-windows is Go on Fyne, so it uses the tray's CLI runner, app discovery and window function as they are, builds with the same toolchain, and runs on X11 and Wayland from one binary (GLFW 3.4). The tray starts one process per window, which exits when the window closes: the toolkit and its OpenGL stack are loaded only while a window is open, and a window that can't open (no OpenGL, a broken display) costs the tray nothing: it falls back to the terminal. One window of each kind is a file lock the kernel releases however the holder exits, with a socket beside it to bring the open window to the front; it needs no session bus and is tested without one. It's a module of its own inside the tray's because Fyne moves shared dependencies (it requires a newer `fyne.io/systray`) and needs cgo: in its own module the tray's dependency versions, binary and CGO-free `go build ./...` stay exactly as they were.

### Rejected
- **GTK 4 through gotk4**: cgo as well, a GTK stack, and much longer builds; Go bindings less settled than Fyne
- **A web UI** (Wails, a WebKitGTK window): a web engine per window, and a second language for the views
- **One resident windows process** the tray talks to: a toolkit loaded all day, or a lifecycle to manage, for windows opened now and then
- **One window of each kind through the session bus** (a bus name, a raise method): needs a bus; the lock works anywhere and is simpler to test
- **The windows in the tray's module**: Fyne's requirements would move the tray's own dependencies, and its CGO-free builds of `./...` would fail on Fyne's cgo

## Decision: the Linux windows app is built in Docker
Fyne needs cgo against Linux's OpenGL, X11 and Wayland headers and libraries, which a Mac doesn't have, and releases are built on my Mac. `make package-linux` builds automator-windows in a Debian 12 container (the Go image of `go.mod`'s version) holding both architectures' libraries (Debian multiarch) and a C cross compiler for the architecture the machine isn't: both build at native speed from one image, with no emulation. Debian 12's glibc makes a binary that runs on glibc 2.34 or later (Ubuntu 22.04, Debian 12, Fedora 35 and newer). Any Docker works (OrbStack, Docker Desktop, Colima, Docker on Linux); Go's caches are kept in Docker volumes. `LINUX_WINDOWS=none` packages without it, for a machine with no Docker: the tray then opens every window in a terminal.

### Rejected
- **fyne-cross**: the same containers behind another tool, with its own images and versions to track, for what one Dockerfile does
- **zig as the C cross compiler**: it brings no OpenGL, X11 or Wayland headers and libraries; they'd still come from a Linux system or packages
- **Building in an OrbStack Linux machine**: tied to OrbStack, and a machine to keep provisioned
- **A container per architecture under emulation** (`--platform linux/amd64`): slower, and dependent on Rosetta or QEMU

## Decision: the Linux tray starts at login from an XDG autostart entry
The installer writes `~/.config/autostart/<Command>-tray.desktop`. Every desktop session runs autostart entries (GNOME, KDE, Xfce, Cinnamon, MATE, LXQt), only graphical ones do, so a server or a headless session never starts the tray, and the tray gets the session's environment (its display, its terminal). Turned off, the entry is hidden (`Hidden=true`), which every desktop honours. The installer starts and stops the tray itself: it finds the running one by its executable, waits for it to exit before starting the new one (as for macOS's login items), and starts it in the environment the old one ran in, so an update run from a shell without a display still restarts it on the desktop. As a backstop, the newest tray takes the app's name on the session bus and the older one quits.

### Rejected
- **A systemd user unit bound to `graphical-session.target`**: several desktops never reach that target, so the tray wouldn't start there; and the update the tray starts (`update --detach`) would run in the unit's cgroup, which restarting the unit kills mid-install
- **The scheduler starting the tray**: it runs as a systemd user unit, outside any desktop session; it would have to find one, and on a headless machine there's none to find

## Decision: Quit stops the scheduler too; opening the app starts it
Quit in the menu bar app or tray stops the app as a whole, the scheduler included: that's what quitting an app means to the people using it, and a scheduler left running behind an app they quit is a surprise. It doesn't turn off starting at login (that's Start at login), so both come back at the next login, as before. Opening the app starts the scheduler if it isn't running, so it's always the way back after a Quit, and opening it while it runs opens the window that matters (Setup while setup isn't done, else Settings), as clicking an app already running does. Both are core commands (`login-items stop|start`), so the two frontends and a terminal do exactly the same: launchd and systemd stay in the core's per-OS files. `stop` spares the process that ran it, so a frontend quits itself after the command answers (and can say why it failed) rather than being killed mid-call; it stops a frontend opened from elsewhere too, so `stop` from a terminal quits everything. On macOS the scheduler is booted out rather than signalled, as KeepAlive would restart it. On Linux the app menu's entry runs a second tray with `--open`, which asks the running one over its bus name and exits, so the choice of window is made where the status is followed; without `--open` the newest tray still takes over, which updates rely on.

### Rejected
- **Quit ends the frontend only**: the scheduler kept running routines with nothing showing it, which isn't what quitting means to anyone
- **Quit turns off starting at login**: quitting is for now; whether it starts at login is a setting of its own
- **Each frontend calls launchctl or systemctl itself**: logic in a frontend, and the two would drift from each other and from a terminal
- **A flag for the frontend that runs `stop`** (stop only the scheduler): the frontend's windows would stay (Linux's are processes of their own), and the caller would have to know it's a frontend; sparing the caller is the same for everyone

## Decision: the scheduler's unit goes where systemd reads it; lingering stays my choice
systemd's user manager reads units from its own `XDG_CONFIG_HOME` (usually none: `~/.config`), not the installing shell's, so the installer asks it where (`systemctl --user show-environment`) and `login-items` looks there too; a shell whose `XDG_CONFIG_HOME` differs gets a note, since the scheduler would then keep its config apart from the CLI's. A user manager runs only while I'm logged in unless lingering is on; outside a desktop session (a server, where that matters) the installer says so and gives the command, but doesn't run it: it needs root, and it's a choice about the machine.

### Rejected
- **The unit beside the shell's `XDG_CONFIG_HOME`**: systemd doesn't read it there, so enabling it fails and the install stops
- **The installing shell's `XDG_CONFIG_HOME` written into the unit**: an update run from the tray, whose session may not have it, would write the unit without it and move the scheduler to another config folder
- **Enabling lingering**: `sudo` in an installer that otherwise never needs it

## Decision: on Linux, runs hold the idle inhibitor logind allows
logind gives sleep inhibitors to sessions; the scheduler runs as a systemd user service, outside any session, and polkit refuses it one ("interactive authentication required") unless an administrator's rule allows it. An idle inhibitor polkit allows anyone, so runs take `sleep:idle` when allowed and `idle` otherwise: logind's own idle action honours it, a desktop's idle suspend may not. A run that suspend cuts off is retried once if it changed nothing, so the gap costs a retry, not a failure.

### Rejected
- **A polkit rule installed with the app**: root, for an installer that never needs it
- **Inhibiting from the tray**: only while a desktop session runs it, and frontends don't act on run status beyond showing it

## Decision: sleep is detected from the clocks, and only a run that changed nothing is retried
Go's monotonic clock stops while macOS and Linux sleep; wall-clock time doesn't, so the difference is time asleep, the same on both with no OS calls. A run that failed after sleeping is retried from the start only when it can't do anything twice: no writes through the gateway and no agent CLI built-in tools, whose effects the app can't see. On macOS a maintenance wake runs apps for seconds and sleeps again with the lid closed, so a scheduled run waits for a full wake instead of being cut off.

### Rejected
- **Resuming the run where it stopped**: the backend's connections and the agent CLI's state don't survive, and tool calls aren't replayable
- **Retrying any failed run after sleep**: one that already posted would post twice
- **Waiting for a wake notification (IOKit, logind's `PrepareForSleep`)**: cgo on macOS, a D-Bus subscription on Linux, for what the clocks already tell

## Decision: opening something doesn't wait for xdg-open to finish
Outside GNOME, KDE and Xfce, `xdg-open` runs the browser itself and returns only when it's closed, so a sign-in waiting on it would wait for the browser. A failure (no handler, no display) shows in its first seconds, so it's given 5 and, still running, taken to have opened it.

### Rejected
- **Waiting for it**: a sign-in that finishes only once the browser is closed
- **Not waiting at all**: a failure would never be seen, and the link never printed for me to open

## Decision: Claude manages routines through the core's own MCP server
People make routines by talking to Claude, so Claude is another frontend: `procauto mcp serve` serves the same surface as the CLI. Every CLI command is a tool by default, derived from the command tree, and one override table holds the exceptions, so the server isn't edited when the CLI grows. The guardrail is my approval rather than missing tools: side-effecting tools are annotated so the client asks first, created routines start disabled and untrusted, and trust stays in my terminal.

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

## Decision: the app's MCP server is its interface, not a plugin
`mcp serve` is how Claude and other MCP clients use the app, as the CLI is in a terminal. Routines don't get it: none needs the app now, and one that did would use a plugin that talks to the MCP server, like any other service. A plugin that needs the app's routines or runs (e.g. to tell a routine's saved sessions from mine) opens a read-only session to it with `Host.ConnectApp`: one way in, the same tools Claude sees, and never the core's files.

### Rejected
- **Serving it to routines as a built-in `automator` server**: a plugin in name only, special-cased in the core for no routine that needs it
- **Plugins reading the data folder**: ties every plugin to the core's file layout

## Decision: MCP sign-in runs across calls
A sign-in can need several pages, and from protocol 2026-07-28 a tool can't ask the client anything mid-call: it returns the page as an input request and the client calls again. So `auth login` runs the sign-in in the background, keyed by the call's request state, and each call returns the next page or the outcome. For older clients the SDK makes those round trips itself, so one implementation serves both. Secrets go through a page in my browser, never through the model.

### Rejected
- **Eliciting mid-call** (`ServerSession.Elicit`): the SDK refuses it on current protocol versions, so sign-in would only ever fall back to opening a browser on this machine

## Decision: dry runs withhold write-capable built-in tools
Agent CLI built-in tools (Bash, Edit, Write, …) run inside the agent CLI, where the tool gateway can't see or intercept their calls. So a dry run gets only the read-only built-ins (Read, Glob, Grep, WebSearch, WebFetch), and the others are left out entirely. On Codex, whose shell and file edits are bounded by its sandbox rather than by tool names, a dry run drops `write` and `network`: the shell stays, in the read-only sandbox without network, which the OS enforces.

### Rejected
- **Intercepting them like MCP writes**: their calls never pass through the gateway

## Decision: the core adds a routine folder's CLAUDE.md (or AGENTS.md) itself
Claude Code loads a folder's `CLAUDE.md` only through its `project` setting source, which also loads that folder's settings and hooks, and only in folders the user has trusted. So runs keep every setting source off, and the core reads `<folder>/CLAUDE.md` and adds it to the system prompt: the same instructions, with no hooks or settings, in any folder. Codex is handled the same way with its own file, `AGENTS.md`: Codex's loading (`project_doc_max_bytes`) is off and the core adds `<folder>/AGENTS.md` to `developer_instructions`. Codex's own loading would also take every `AGENTS.md` (or `AGENTS.override.md`) from the git root down, and depends on project trust, which runs don't read: the core's is the folder's one file, the same in every folder, as on Claude Code. Each CLI gets its own file, as a person using that CLI in the folder would.

### Rejected
- **`--setting-sources project`**: a folder's hooks would run unattended commands, and its settings could widen permissions
- **Codex's own AGENTS.md loading**: files above the folder join in, and whether any load depends on trust kept in the config runs ignore
- **One file for every backend** (e.g. `CLAUDE.md` on Codex too): a folder set up for Codex keeps its instructions in `AGENTS.md`

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
- **A usage limit on Codex**: `codex exec` reports no plan usage, and Codex's own status is interactive only, so `codex` has none (`kind: none`); a run that hits Codex's limit fails and says so. Counting its runs against the API budgets would mix in an earlier backend's spend

## Decision: secrets through the OS's own command-line tools
The Keychain is reached by shelling out to `/usr/bin/security` and the Secret Service by `secret-tool` (libsecret), values on stdin and base64-encoded, behind one `secrets.Store` interface. No cgo and no D-Bus library in the core; `secret-tool` is a package away on every desktop distribution.

### Rejected
- **cgo Keychain bindings**: cgo in an otherwise pure-Go core
- **A Go Secret Service client** (godbus, go-keyring): a new dependency tree for four calls
- **An encrypted file**: its key would sit next to it, or need a password at every run

`secret-tool lookup` prints nothing both when there's no such item and when its keyring is locked and can't be unlocked (no prompt can be shown), so an empty lookup is checked with `secret-tool search`, which lists locked items: a locked keyring then says so, instead of looking like a sign-in never made

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

## Decision: CI and releases run on my Mac
`make ci` runs the checks and `make publish` builds, signs, tests and publishes a release, both locally; the GitHub Actions workflows are disabled. Actions minutes ran out, and every check but the Linux install test runs as well on a Mac. The workflows are kept, so turning them back on is `gh workflow enable`.

### Rejected
- **Keeping CI on GitHub for the Linux install test alone**: the minutes, for one check
- **Deleting the workflows**: bringing them back means rewriting them, secrets and deploy key included

## Decision: templates name the backends they work with
A template can list the backends it works with (`automator.backends`), and installing it on another backend is refused, saying how to switch. Some routines only make sense on an agent CLI (built-in tools, a folder, a saved session, effort): on `claude-api` or `local` they'd install and set up fine, then fail every run. Setup only offers templates that can be installed; `templates list` still shows the others, marked, since the backend can change.

### Rejected
- **Working it out from the fields a template uses**: implicit; a template author states it once instead
- **Hiding them from `templates list`**: switching backends would make templates appear from nowhere

## Decision: Codex runs through `codex exec`
The `codex` backend drives OpenAI's Codex CLI headless with `codex exec`, like Claude Code's `claude -p`: one `CLITool`. `codex exec` has what a run needs: JSONL events (`--json`) with the thread ID, the final message, token usage and errors; isolation from my setup (`--ignore-user-config`, which still uses the sign-in in `CODEX_HOME`; `--ignore-rules`); MCP servers from `--config` with a bearer token read from an environment variable and `required`, so a run that can't reach its tools fails; developer instructions that add to Codex's own; an output schema file; ephemeral sessions; an approval policy that never asks. The routine's MCP server's tools are approved up front (`default_tools_approval_mode = "approve"`): under `never`, Codex refuses any tool not marked read-only that would need an approval, and the gateway already decides what runs. The accepted costs: Codex's global `AGENTS.md` in `CODEX_HOME` still loads (only another `CODEX_HOME` turns it off, and that would leave the sign-in behind); runs read the sign-in from `auth.json`, Codex's default store, since the config that could pick the keyring isn't read; only `model` applies from the frontmatter, and only OpenAI models; no turn limit (the timeout bounds a run); and no usage limit in the app.

It's labelled experimental and untested wherever a backend is chosen (the setting's description, setup's status, the installing agent's instructions), from a flag on its backend definition: it has run only against a stand-in for OpenAI's Responses API (Codex's own custom model provider support), never against OpenAI, so someone choosing it knows that before relying on it.

### Rejected
- **An ACP adapter** (Agent Client Protocol): Zed's Rust `codex-acp` is archived, and its replacement is a TypeScript translation layer from npm: another runtime and package between the app and Codex, for what `codex exec` does directly
- **The Codex TypeScript SDK**: it runs `codex exec` itself; the same interface behind a Node runtime
- **`codex app-server`** (Codex's JSON-RPC server): a long-lived conversation protocol for IDEs; a run is one prompt to one result
- **Loading my Codex config**: its MCP servers, profiles, hooks and project trust would change what a routine can do
- **Another `CODEX_HOME` per run**: the sign-in lives there, and copying it would race Codex's token refreshes

## Decision: Codex's built-in tools are named by what they allow
Codex has no list of tools to allow by name: its shell is one tool whose reach is set by a sandbox (`read-only`, `workspace-write`, network on or off), and its file edits come with the model. So on `codex`, `builtinTools` names capabilities in Codex's terms (`shell`, `write`, `network`, `web_search`, `view_image`) that map to Codex's feature toggles and sandbox. Without any, a run gets only the app's tools as far as Codex allows: its shell is off and the sandbox read-only, which stops its file-editing tool from writing. Each backend checks its own names, at run time and in readiness, so a routine whose names don't fit the selected backend shows up in `doctor` instead of failing its next run.

### Rejected
- **One vocabulary for every backend** (mapping Claude Code's Read, Bash, Edit onto Codex): Codex can't allow reading without the shell, nor editing without commands writing too, so the mapping would promise what Codex doesn't enforce
- **Codex's `danger-full-access` sandbox**: nothing would bound what a run's commands change

## Decision: a session's ID comes from whichever side picks it
Claude Code saves a session under an ID it's given (`--session-id`), so the app picks it before the run and it's on record while the run runs. Codex picks its own (the thread ID in its first event), so a CLI tool can say it picks its IDs (`SessionPicker`): the app gives it none, and records the one it reports when the run ends, failed runs included. The engine knows only which side picks, never which CLI. Each run also records its backend (`lastRun.backend`, history's `backend`), so a frontend knows which CLI resumes a session.

### Rejected
- **Resuming Codex sessions by `--last`**: another session in the same folder (mine, or another routine's) would be resumed instead
- **Parsing the ID from Codex's session files**: Codex's files, not its interface

## Decision: commands bring run status up to date
Run status is the scheduler's record, but a command that changes something also records each routine's state there when it succeeds, as the scheduler's pass would. Otherwise turning a routine on in the setup window left it showing as paused for up to a pass (30 seconds). Commands run read-only (`setup --json`, `run --dry-run`) don't write it, so frontends that poll them don't make it change.

### Rejected
- **Only the scheduler writes routine state**: what I just did shows late
- **Frontends showing the change themselves until the next pass**: they'd need the core's logic

## Decision: saving sessions doesn't need a folder
`session: save` and `folder` are separate settings. Claude Code files a session under the folder it ran in and resumes it only from there, so a routine that saves its sessions without a folder works in an empty folder of its own (`<data>/session-folders/<name>/`) that's kept, not a temporary one. Each run records the folder it worked in (`folder` on `lastRun` and in history), so a frontend resumes or forks it from there. The trust surface is still the `folder` setting, so turning saving on or off doesn't ask for trust again.

### Rejected
- **`session: save` needs a `folder`**: two separate choices tied together; a routine that works on no project had to pick a folder only to keep its sessions
- **Keeping the temporary folder while its session exists**: the app would have to track when Claude Code drops a session

## Decision: a paused routine is never unfinished setup
Once a routine is trusted, it has been turned on once; pausing it after that is my choice. Its setup step only offers to resume it, and never counts as unfinished, so pausing or resuming a routine never turns the light red.

### Rejected
- **Required while no other routine runs on its schedule**: pausing my last running routine turned the light red, as if setup were broken

## Decision: setup's state is recorded by whatever computes it
Run status's `setupLeft` (the menu's Finish Setup…) is refreshed after every command that isn't read-only, after `usage` and after `setup --json`, not only after a list of commands known to change setup. The list missed some (reading the plan's usage again), so the menu kept Finish Setup… while the setup window had nothing left. Working out what's left only reads files, and run status is written only when it changes.

### Rejected
- **A list of the commands that change setup**: it goes stale whenever a step depends on something new
- **Frontends computing setup themselves**: the menu would need setup's logic; it only shows the core's output

## Decision: /usage can't be read only after it keeps failing
Setup's `usage` step comes up when `/usage` answered without a usage report the app can read, or 3 tries in a row failed. One failed try (a slow start while a routine runs, its rate limit) used to make it a setup step that the next reading took away.

### Rejected
- **The last try decides**: setup flickers with every timeout

## Decision: macOS releases are for Apple silicon only
A release builds the macOS app for Apple silicon only: building automator-ui for Intel too doubled the release build, for Macs nobody using the app has. The installer and `update` say so on an Intel Mac instead of installing an app that can't run.

### Rejected
- **A universal app**: twice the build time, for no one

## Decision: releases can be self-signed
Without an Apple Developer account, releases are signed with a self-signed certificate kept in CI, the same for every release. Downloaded by `install.sh` or `update`, the app carries no quarantine, so Gatekeeper doesn't check it; and since macOS ties grants to the certificate, they survive updates. With `TEAM_ID` empty, installs accept any valid signature, so the checksum is the only check and a release swapped by someone who can write to the repository isn't refused. Setting up a Developer ID later is adding its secrets and `TEAM_ID`.

### Rejected
- **Ad-hoc signing**: a new identity every release, so every grant and Keychain item would be asked for again after each update
- **Waiting for a Developer ID**: no release anyone else can install until then

## Decision: public releases are read from GitHub's web pages, with no sign-in
Process Automator's releases are public, so installing and updating need no GitHub account and no `gh`. `install.sh` (with curl) and `update` read them as a browser would: the latest release is the tag `https://github.com/<repo>/releases/latest` redirects to, its files are downloaded from `https://github.com/<repo>/releases/download/<tag>/<name>`, and its `SHA256SUMS` is its list of files. No token is sent, there's no API rate limit, and the checksum and signature checks are those of private releases. Whether an app's releases are public is its own choice (`PublicReleases`, `PUBLIC` in `install.sh`), and private is the default, so an app built on the engine that keeps its releases private reads them signed in, as below. Every release carries its app's `install.sh`, so a public app installs with one line from its latest release.

### Rejected
- **The GitHub API without a token**: 60 requests an hour per IP address, shared by everyone behind it, and a refusal reads like a missing sign-in
- **A separate public repository for releases**: a second repository to publish to and keep in step, for files the app's own repository can serve
- **Other hosting** (object storage, a CDN, a website): an account and a bill to keep, and one more place a release could be swapped, for files GitHub already serves
- **Trying the public pages first, then signing in**: every install of a private app would start with a failing request, and a public app's errors would still talk about signing in
- **Public by default for every app**: an app with private releases would stop updating as soon as it moved to an engine version with that default

## Decision: private releases are read with the GitHub CLI's sign-in
An app whose repository is private needs a GitHub token to read its releases. `update` and `install.sh` use `GH_TOKEN`/`GITHUB_TOKEN` when set, else the GitHub CLI's sign-in, and no token otherwise. Anyone invited already signs in to GitHub to get the code or the tap, so there's no new credential, and the app never stores one. `gh` can be signed in to several accounts, and the active one is whatever was last switched to for other work, so they try each account until one can read the repository rather than only the active one.

### Rejected
- **A `github` sign-in in the secrets store**: one more secret to hold, ask for and refresh, for access people already have through `gh`
- **Public release assets from a private repository**: GitHub doesn't offer it; a separate public releases repository would share the app beyond invited people
- **Only the active `gh` account**: an update failed whenever another account was active, though one signed in could read the releases
- **Switching `gh`'s active account**: changes the person's setup for every other tool

## Decision: `update` replaces the app where it's installed
An update installs over the running app's own location, whatever installed it, so updating from automator-ui works for an app installed by `install.sh`, `make install` or the Homebrew cask (which declares `auto_updates`, so brew leaves it to the app). Only the Homebrew formula's install, in brew's Cellar, is updated with `brew upgrade`.

### Rejected
- **Always `brew upgrade` for Homebrew installs**: automator-ui would need brew and its token in its environment, and the cask's app can't be told apart from one installed by hand
- **Always building from a source checkout**: needs Go, Xcode and a checkout on every machine, and signs with a local certificate; it remains only the fallback when the releases were read and none has a download for the machine
- **Building from the checkout whenever releases can't be read**: a missing sign-in or a network error turned into a build of minutes, from whatever the checkout's tags were, instead of saying what was wrong

## Decision: an update's progress is a file the installing process keeps
An update runs detached from whoever started it (automator-ui restarts during it), and a build from the checkout takes minutes, so the process installing writes its step and the latest line of its output to `update-progress.json` as it goes, and `update progress` reports it. A frontend asks for it every few seconds while one installs; the lock still says whether one is.

### Rejected
- **Reading `update.log`**: frontends read no file of the core's, and a log says what was printed, not what step it's at
- **`update progress --watch`, a stream like `status --watch`**: a second long-lived process for something that lasts minutes, a few times a year
- **Progress in run status**: the scheduler writes run status; an update isn't a run, and runs without the scheduler

## Decision: development builds keep one signing identity
Every local build is signed with the same self-signed certificate, with a stable bundle ID and install path, and reinstalling restarts the apps in place. macOS ties permission grants and Keychain access to the signing identity, so rebuilding never asks for them again.

### Rejected
- **Ad-hoc signing**: a new identity every build, so every grant and Keychain item would be asked for again

## Decision: my own model server is a backend of its own, `local`, apart from `claude-api`
Local and other vendors' models (Ollama, llama.cpp's `llama-server`, vLLM, LM Studio; DeepSeek, Qwen, Kimi) all serve OpenAI's Chat Completions API, so Genkit's `compat_oai` plugin runs them, pointed at one URL (`llm.local.url`): one setting covers every such server, and the Genkit loop, gateway, dry runs and outcome rules stay the same. One server is enough for now: a second would be a named endpoint later, without changing this one's setting.

They used to run on the backend called `api`, picked per routine by an `openai-compat/<model>` model, so one backend ran both Claude on Anthropic's API and Ollama, and its name said neither. Now each backend names what runs routines: `claude-api` is Anthropic's API, `local` is my own server. On `local`, a routine runs on the model I set for the server (`llm.local.model`) whatever its `model:` says: routine files and templates name Claude models, which no local server has, and there's no sensible mapping from a Claude model to whatever I've pulled, so switching backends needs no routine edited. A routine that needs a particular local model names it as `local/<model>`, which `local` honours. `claude-api` keeps running `local/<model>` routines on the same server with the same settings and key, so a cheap routine can run locally beside Claude ones; that's why `llm.local.url` applies to `claude-api` too (`AlsoBackend`), while `llm.local.model` doesn't.

The rename keeps what's already set working, with nothing to do: `api` in the config or a routine's `automator.backends` reads as `claude-api` (an enum alias, normalized wherever values are read and validated, so the file a person wrote stays as they wrote it), `llm.openai-compat.base-url` is a retired setting read as `llm.local.url` (rewritten at the next settings change, like any retired setting), the key stored under `openai-compat` is read while `local`'s isn't stored, and `openai-compat/<model>` is `local/<model>`. Older runs recorded as `api` still count toward the budgets.

Readiness checks what costs nothing (a URL, a model name, a key) wherever routines are judged, the scheduler's pass included; connecting to the server is left to `doctor`, `routines check`, the interactive setup and the run itself, since the scheduler judges every routine every 30 seconds and `setup --json` runs in that pass too. A server that's down then shows in the run's failure and in `doctor`, not as a routine that can't be scheduled. So a key can't be required up front: only the server answering 401 says it needs one. On `claude-api`, the Anthropic key stops being required when every routine runs on the server, since a local-only setup has no use for one.

Its runs aren't priced: a local model costs nothing, but a hosted one does at a price the app doesn't know, so they count as unknown rather than free, and never toward the Anthropic budgets. On this server, tool errors (bad input, a tool that doesn't exist) go back to the model, as on agent CLI backends: smaller models make these mistakes, and the outcome rules still fail a run whose write didn't happen. It's labelled experimental and untested wherever it's offered, as `codex` is: it has been tried only against a stand-in for the API and small models on Ollama, whose tool calling is unreliable.

### Rejected
- **One `api` backend for both, picked by the routine's model** (as it was): the name covered two unrelated things, and running locally meant rewriting every routine's model
- **Mapping Claude models to local ones** (e.g. Haiku to a small model): there's no right answer for what I've pulled, and one model set once is simpler to reason about
- **Refusing routines that name Claude models on `local`**: every template does, so nothing would run
- **Dropping the per-routine route on `claude-api`**: it's cheap to keep (the same server and settings), and mixing a local routine in with Claude ones is a real use
- **Rewriting `api` to `claude-api` in the config file**: a person's file changing under them is surprising, and an alias costs a map entry
- **Genkit's `ollama` plugin**: Ollama only; `compat_oai` covers Ollama through its OpenAI-compatible API, and every other server too
- **Checking the server in the scheduler's pass**, with a short timeout and a cache: a slow or blackholed server would still stall a pass, and the run reports the same failure
- **Counting its runs as free**: a hosted API's spend would vanish from the record

## Decision: the local server keeps a model briefly after each request
Every chat request to the local server carries `keep_alive` (`llm.local.keep-alive`, 1 minute), Ollama's extension to the OpenAI API, so a model's memory is freed a minute after a run instead of after Ollama's 5 minutes. Each request resets that time, so a run keeps its model loaded between turns, and so does anyone else using the model (a chat app's own requests). Ollama can't tell a client whether a model is in use (`/api/ps` has no such field), and unloading waits for requests in progress but then makes every other client reload. Servers that don't know the field ignore it; one that refuses it gets the request again without it.

### Rejected
- **Unloading the model after a run** (an empty request with `keep_alive: 0`): it would unload a model someone is chatting with
- **Asking `/api/ps` first**: it says what's loaded and until when, not whether anyone is using it
- **`OLLAMA_KEEP_ALIVE`**: the server's own setting, for everything on it, not the app's to change


## Decision: the guide is in the binary, generated from code where it can be
How to write a routine and how to add a plugin is a command, `guide`, so every installed app has it at its own version, and MCP clients read it through the server like any other tool. What the code defines (the routine keys, each backend's models and built-in tools, the inputs, `plugin.yaml`'s fields) is generated into it, so it can't drift from what the app accepts; the rest is written once, next to the code, kept to what's needed to get it right the first time (about a screen per topic, besides the tables), and its examples are files the tests build, install and create. The guides in `docs/` link to it and hold the rest. Over MCP it's one tool with the topic as an enum argument, served as text: one entry in the tool list, whose description names the topics, and prose the model reads as it is.

### Rejected
- **Shipping the docs folder with the app**: written for people reading a vault (wiki links, anchors across pages), with nothing generated, so it drifts; and a client would have to find and read files
- **Pointing to the repository**: installs don't have it, it may be ahead of or behind the installed version, and an agent may not reach it
- **One MCP tool per topic**: more tools in every client's list for one command, and each new topic another override
- **Serving it as `--json` over MCP**: the same text, escaped, with its tables repeated as data
- **Putting it in the MCP server's instructions**: they're sent with every session; the guide is read when it's needed

## Decision: programs are a second kind of routine, behind one interface
Some scheduled work needs no model: a program I wrote (usually in Go) does it the same way every time. It still wants everything around a run the app already has: a schedule, catch-up after sleep, pause, stop, run history, notifications, the status dot, trust. So a program is a routine of another kind, not a feature beside routines: `engine.Runnable` is what that machinery works on, an LLM routine and a program each implement it in their own file, and the split happens only at the step that runs (the agent for one, exec for the other) and at what each kind checks and trusts. "Routine" stays the umbrella word in commands (`routines list --kind program`), with an LLM routine as its default sense, so the commands people know keep working and a name finds either kind.

### Rejected
- **One struct with a kind field**, branched on wherever kinds differ: the smell `pluginapi.Plugin` had, with fourteen branches on its own kind before it was split
- **Parallel commands per kind** (a `programs` group with its own run, stop, pause, history): twice the commands for one machinery, and a frontend or Claude would need to know a name's kind before acting on it
- **A program as a fifth model backend**: backends run models; a program has no prompt, tools, MCP servers or tokens, and a backend is chosen for every routine at once
- **A generic "command" field in a routine file**: a prompt file that's really a shell command would carry an allowlist, model and memories that mean nothing for it

## Decision: no kind means an LLM routine, at every level
The app is for LLM routines; programs are the add-on, named explicitly. So `kind` is absent wherever an LLM routine is meant: a CLI flag left out, an MCP argument, a JSON field, a run record, a trust record, a file. Everything written before programs reads the same, LLM routines' outputs and records are written exactly as before, and only where no name is given (listing, checking all, creating) does the default matter; a name finds either kind. Frontends ask for `--kind all` once, so programs and LLM routines show in the same lists.

### Rejected
- **Writing `kind: llm` everywhere**: every existing output and record would change for a fact that's the default
- **Listing both kinds by default**: a bare `routines list` would contradict "routine means LLM routine", and a script reading it would meet rows without a prompt
- **A second name space for programs**: two things named `backup` couldn't be told apart by `run backup`

## Decision: a program is trusted by checksum, and what runs is the app's copy
A program has no tool allowlist and no writes the app can intercept: trust is its only guard. Adding one copies its description file and executable into the app's own folder, and that copy is what runs, so rebuilding or swapping the file where I made it changes nothing until I update the program; trust records the SHA-256 of the copy and of the description file (its arguments, `writes` and secrets with it), and the copy's checksum is taken again right before each run. Another executable or a changed description takes the trust away, said in words (another executable, now writes, a secret added). A program's secrets are its own keys, passed only as the environment variables it declares.

### Rejected
- **Running the file where I built it**, trusted by path: a rebuild or a swap between the check and the run would run untrusted code
- **Trusting the executable only**: a new secret or `writes: false` in the description would change what it's given and how dry runs treat it, unreviewed
- **Checking the checksum only when trust is given**: the copy in the data folder can still be changed afterwards; checking it at each run costs a hash
- **Giving it the app's own environment**: it would see the app's own keys and whatever launchd, systemd or the terminal happened to set

## Decision: a program's dry run is --dry-run by convention, and needs trust
The app can't intercept what a program does, so a dry run is the program's own: one that declares `writes: true` is passed `--dry-run` and is expected to change nothing; one that doesn't write runs as it is. Either way the executable runs, so unlike an LLM routine's, a program's dry run needs trust: a dry run of an untrusted program would run untrusted code, and `--dry-run` is only a convention the program may ignore. `writes` is required in the description file, so a program can't be taken for read-only by leaving it out.

### Rejected
- **Dry runs need no trust, as for LLM routines**: they'd run any executable added, unreviewed, including through Claude
- **Refusing dry runs of programs**: the convention is useful, and frontends offer Dry Run for every routine
- **An environment variable instead of the flag**: a flag is what a command-line program already parses, and shows in its usage
- **`writes` defaulting to false**: a program that forgot to say would get no `--dry-run` on a dry run

## Decision: programs skip the run queue and the usage limit
The one-at-a-time run queue exists because of the model's plan and usage limits, and the usage limit is about LLM spend; neither applies to a program. A program takes only its own routine's lock (it never overlaps itself), so a backup isn't held up behind a long digest, and a digest isn't held up by a backup.

### Rejected
- **Programs waiting in the queue**: a quick program would wait for a model run it has nothing to do with, and could go stale doing so
- **A queue of their own**: nothing they share needs taking turns

## Decision: a program runs in a defined environment, in a process group of its own
A program started by the scheduler under launchd or systemd would otherwise see a minimal, changing environment, and a program started in a terminal the terminal's. It gets the same one every time: a fixed PATH per OS (the system's folders, and Homebrew's on macOS), my home folder, a temporary folder, its secrets and what the run is for, in its own data folder, which is kept between runs. It runs in a process group of its own, asked to end and then killed as a group on stop, timeout and shutdown, and whatever it leaves running when it exits is killed too, so a run never leaves processes behind. Its output is read through a pipe the app owns, kept and logged up to 64 KiB: a runaway program can't fill the log or hold the run open.

### Rejected
- **The login shell's PATH**, as agent CLIs and MCP servers get: whatever my shell profile does would change what a scheduled program finds; a program needing more names it in `args` or by absolute path
- **Killing only the program**: what it started (a child process, a server) would outlive its stop or timeout
- **Keeping all of its output**: one program printing in a loop would fill the history

## Decision: a program reports through its exit code and a one-line file
Exit code 0 is success, as every command-line program already says; a one-line summary written to the file `$PA_RESULT` names is what the run's record shows (what it did, or why it failed), and without one a failure shows its output's last line. That's all a program needs to know about the app to fit in, and a program written for the app still runs by hand.

### Rejected
- **Reading the summary from stdout's last line**: a program's normal output would have to end just so
- **A JSON protocol on stdout**: more than one line of status needs, and it couples every program to the app

## Decision: Claude can run programs, but not add or trust one
Through the app's MCP server Claude can list programs, run, stop and pause them and read their runs, as for any routine. Adding one is mine, like trusting one: a program is added from its description file's path, which no tool takes (`--from` isn't exposed, and a routine's source sent on stdin has no folder to find an executable in), and `routines create`'s `--kind` isn't a tool argument. Trusting stays out of MCP for both kinds.

### Rejected
- **Letting Claude add programs, untrusted**: an executable Claude chose or built would sit one trust away from running, reviewed only by a checksum I can't read
