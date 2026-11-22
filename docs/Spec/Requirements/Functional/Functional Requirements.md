Back to [[Home]]. See also [[Spec/Requirements/Functional/Agentic Functional Requirements|Agentic Functional Requirements]], [[Spec/Requirements/Functional/Distribution|Distribution]], [[Spec/Requirements/Technical/Technical Requirements|Technical Requirements]].

## Platforms
- **macOS and Linux**: the same CLI, scheduler and routines on both
- **A menu bar app on macOS, a tray on Linux**: macOS's menu bar app (automator-ui) has the windows too; Linux's tray has the same icon, menu and notifications and the same windows

## Routines
- **Routines are files on my device**: the app ships with none; a routine is added or changed by adding or editing its file in the routines folder, with no app update
- **What a routine says**: what the model should do (the prompt), the servers it uses and the tools it may call, its schedule, and optional settings (below)
- **Schedule per routine**: a cron schedule in local time, changeable without editing the file; a routine without one runs only by hand
- **Pause and resume**: any routine can be stopped from running on its schedule and started again without removing it
- **Run now**: any routine can be run on demand, in the terminal or in the background, as a dry run or for real
- **Backfill**: any routine can be run as of a past day (e.g. Monday's date, for a routine covering the previous workday, covers Friday)
- **Stop**: a run in progress or waiting its turn can be stopped; it's recorded as stopped, not failed
- **Missed runs**: if the computer is asleep or off at a scheduled time, the routine runs when the computer is next fully awake (not in a Mac's maintenance wake, which sleeps again within seconds), unless that's later than the routine's staleness window; then it's recorded as missed
- **Sleep mid-run**: a run cut off by the computer sleeping (its connections drop) runs again from the start, once, if it changed nothing yet
- **Create, update and delete**: from a file, validated first (its servers must exist and offer its tools, when they can be asked); deleting a routine removes its settings, trust and memories and keeps its run history
- **Optional per-routine settings**: each one left out keeps the default behaviour
  - **Timeout**: how long a run may take before it's stopped and fails
  - **Effort**: how hard the model thinks
  - **Folder**: the folder the run works in instead of an empty temporary one (e.g. a repository it checks), with its project instructions (`CLAUDE.md`, or `AGENTS.md` on Codex) as instructions
  - **Built-in tools**: which of the agent CLI's own tools the routine gets, in that CLI's terms (Claude Code's Read, Bash, Edit; Codex's shell, write, web search); none by default
  - **Save session**: keep the run's agent session and record its ID and backend with the run, so I can resume it with that CLI
  - **Concurrency**: wait its turn (default: one run at a time, manual runs included) or run alongside other runs. A routine never overlaps itself; a run waiting its turn shows as queued
  - **Parameters**: values that make a shared routine mine (a channel, a team), asked for at install and changeable later
  - **Platforms**: the operating systems it runs on

## Programs
- **A program is a routine that runs an executable instead of a model**: usually a binary compiled from Go, with a description file (`program.yaml`) giving what it runs, whether it writes, and optionally its description, arguments, schedule, staleness, timeout and secrets. "Routine" with no kind means an LLM routine; programs are named explicitly
- **Kept apart, run alike**: programs live in their own folder, apart from the routines folder, and share everything around a run with LLM routines: schedule and catch-up within staleness, missed runs, waiting for a full wake, keeping the computer awake, run now, backfill, pause, stop, timeout, trust, run status, history, notifications and the status dot
- **One name space**: names are unique across both kinds; adding a routine, a program or a template under a name either kind has is refused
- **Commands take either kind**: those given a name (run, start, stop, show, update, delete, pause, resume, trust, runs, settings) find it whatever its kind; where no name is given (listing, checking all, creating), no kind means an LLM routine, and `--kind program` or `--kind all` says otherwise. What only LLM routines have (memories, an LLM routine's settings) given a program's name fails with "X is a program"
- **Adding one**: from its description file, with the executable it names beside it; both are copied into the app's own folder, and that copy is what runs. It starts paused and untrusted. Updating one copies them again; deleting one removes its copy, settings, trust and secrets, and keeps its run history and data folder
- **Trust**: a program runs only while its executable and description file are the ones I trusted, dry runs included
- **Dry runs**: one that declares it writes is passed `--dry-run`; one that doesn't runs as it is
- **What it runs with**: the secrets it declares, as environment variables (signed in with `auth login <program>`), a fixed PATH, my home folder, and its own data folder as its working folder; nothing else from the app's environment
- **Its outcome**: exit code 0 is success; a one-line summary it writes to the file named by `$PA_RESULT` is the run's reason; what it prints is kept with the run, up to a limit
- **Never in the run queue**: programs don't wait for LLM routines' runs, and aren't held back by the usage limit
- **Ending it**: stopping it, its timeout or the scheduler stopping ends it and everything it started
- **Sleep mid-run**: one that doesn't write runs again once after the computer slept during it; one that writes never does

## Templates
- **Templates are shareable routines**: files in the repository's `templates/` folder, shipped with the app as files; the app ships none of its own and works with none
- **Tags**: templates are grouped in tag folders, general to specific; those under `projects/<project>/` are for that project's members
- **Installing**: a template is copied into the routines folder, where it's mine to edit; its parameters are asked for and become per-routine settings
- **Per OS**: only templates for this OS are offered unless I ask for all

## Plugins
- **A plugin is an MCP server**, written in any language: remote (OAuth, an API key in headers, or public) or a local command, or a folder of my own with a `plugin.yaml`
- **Adding one** needs no code; it's tried first and nothing is added if it doesn't work. Removing one forgets its credentials
- **The `notify` plugin**: desktop notifications from routines; this app compiles it in
- **Sign-ins** are done once and refreshed unattended; one that can't be refreshed is reported as needing a sign-in, with the server named, and fixed from the CLI or the UI

## Model backend
- **Four backends**, chosen by a setting: Claude Code on my Claude plan (`claude-code`), Anthropic's API with my API key (`claude-api`), my own model server (`local`: Ollama on my machine by default, or another local or hosted OpenAI-compatible one, with an API key if it needs one), or OpenAI's Codex CLI on my Codex sign-in (`codex`). `local` and `codex` are experimental and untested, and said so where they're chosen
- **Routines name Claude models**: on `local`, every routine runs on the one model I set for the server, unless it names another model there (`local/<model>`). On `claude-api`, a routine that names `local/<model>` runs on my server too; routines that only do need no Anthropic key
- **Former names keep working**: a config, routine or template that says `api` (now `claude-api`), and the server's former setting, key and `openai-compat/<model>` models, work as before, with nothing to change
- **Usage limit, by backend**: on my Claude plan, scheduled runs don't start while the plan's 5-hour or weekly usage is above a share I set; with an API key, they don't start once API spend in a short rolling window or the last 7 days passes the budgets I set (runs on my own server have no price and don't count); on my own server and on Codex there's none (a run that hits Codex's own limit fails, saying so). A run that doesn't start is recorded as skipped with the reason (red indicator, failure notification); runs I start by hand still run, with a warning. `usage` shows where I stand
- **No cost for plan runs**: on Claude Code or Codex the app never works out a dollar cost

## Runs
- **Inspecting runs**: which ran, how each ended and why, with its inputs, tool calls, writes and final text, from the CLI and the UI; also as a readable report
- **Run history and logs** are kept for a retention period I set
- **Outcomes**: success, failed, needs a sign-in, missed, skipped (over the usage limit) and stopped
- **Structured results**: a routine can declare the shape of its result, so history and the UI show clean results instead of prose
- **Health check** (`doctor`): explains a red indicator, with a fix for each problem
- **Routine check**: whether a routine would run (its file, settings, servers connecting, every allowed tool existing, and its model's provider set up and, for my own server, answering with the model), without calling the model

## Notifications
- **From routines** (the notify plugin): a routine can send me a short desktop notification, a title and a message, shown by the plugin itself through the OS (macOS, as the app; Linux, through the desktop's notification service), not by the menu bar app. An **urgent** one breaks through Focus/Do Not Disturb where the OS allows it and stays until dismissed; normal ones are quiet and fade. Clicking one does nothing. It has no settings, app-wide or per routine
- **The app's own**: a desktop notification when a run fails, needs a sign-in, is missed or skipped, saying which routine, when and why (can be turned off), and when an update installs or fails, shown by the menu bar app on macOS and the tray on Linux; clicking one opens the app: on the run it's about, in its routine's window, or (an update's) the app's Routines window. They don't depend on the notify plugin
- **On Linux** clicking one opens its run's report, or (an update's) the Routines window

## Status indicator
- **Icon** in the macOS menu bar, or the Linux tray, with a status dot:
  - **Green**: every enabled routine is fine. A routine with no runs yet is fine while its next run is ahead
  - **Red**: something needs me: a routine failed, was missed or skipped, needs a sign-in, can't be scheduled, didn't start at its time or is stuck; setup isn't finished; or the scheduler isn't responding
  - **Grey**: no routines are enabled
  - **Pulsing**: a routine is running (whatever the colour); in the Linux tray the dot dims and brightens
- Updates within seconds of anything changing

## UI
Managing the app is possible entirely from macOS's menu bar app (automator-ui), without a terminal. The CLI does all of it too; the UI is another way in, not a replacement.
- **Menu** (the menu bar app's and the Linux tray's): the version; the status in words (each reason the dot is red, opening the run it's about); Finish Setup… while setup has steps left; **Routines ›** (all routines, then each with its status); updates; Settings; Quit
- **Quit** stops the app: the menu bar app or tray, and the scheduler (no routine runs on its schedule until I open the app again); both start again at the next login. When the scheduler can't be stopped, it says why and quits anyway
- **Opening the app** starts the scheduler if it isn't running, so opening it after a Quit brings everything back; opening it while it runs opens the Setup window while setup has steps left, else the Settings window
- **Routines window**: every routine, LLM routines and programs together, with its health, schedule in words, last run and status
- **A routine's window**:
  - status: last run (when, outcome, reason), next run, why it can't be scheduled
  - its schedule in plain words (e.g. "At 09:20, Monday through Friday"), edited with a days-and-time picker or a custom cron
  - what it can reach, and trusting it
  - its runs: what each did, its tool calls and writes, and its output
  - controls: run now or dry run, stop, pause/resume, delete
  - its memories (see, add, remove) and its own settings
  - for a program: what it runs (its executable, whether it writes, its arguments and secrets) in place of what it can reach, and no memories or model
- **Setup window**: every setup step with a button for each
- **Settings window**: start at login, LLM usage, and every app-wide setting with a control for its type
- **Updates**: see whether a newer release exists, and install it
- **Start at login**: on or off
- **Linux tray**: the same menu, updates, notifications and windows (Setup, Settings, Routines, a routine's, Updating); a routine's schedule is edited as its cron setting, with no days-and-time picker. Installing an update is confirmed in a notification (where notifications have no buttons, in a window that asks). With no display to show a window on, or without the windows app, a window is its CLI command in my terminal (Setup: `setup`; Settings: `settings list --scope app`; Routines: `routines list --kind all`; a routine: `routines show <routine>`; Updating: `update progress`; asking before an update: `update --ask`)

## CLI
- **Everything is a command**: every feature, including what the UI does, with `--help` on each
- **Settings**: one generic `settings list/get/set/reset` surface reads and writes any setting, app-wide or per routine; values are checked before they're saved and apply without a restart
- **Paths**: `paths` shows where the app keeps its config, routines, run status, history, notifications, templates and the frontends' log
- **Guide**: `guide` explains, from the app itself, how to write a routine (`guide routines`) and how to add a plugin or write my own (`guide plugins`), with examples that work, as text or `--json`; `routines create`, `routines update` and `plugins add` point to it
