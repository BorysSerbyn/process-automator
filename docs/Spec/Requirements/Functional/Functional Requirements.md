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
- **One name space**: names are unique across both kinds, and hooks and watchers (see [[#Hooks]]); adding a routine, a program or a template under a name one of them has is refused
- **Commands take either kind**: those given a name (run, start, stop, show, update, delete, pause, resume, trust, runs, settings) find it whatever its kind; where no name is given (listing, checking all, creating), no kind means an LLM routine, and `--kind program` or `--kind all` says otherwise. What only LLM routines have (memories, an LLM routine's settings) given a program's name fails with "X is a program"
- **Adding one**: from its description file, with the executable it names beside it; both are copied into the app's own folder, and that copy is what runs. It starts paused and untrusted. Updating one copies them again; deleting one removes its copy, settings, trust and secrets, and keeps its run history and data folder
- **Trust**: a program runs only while its executable and description file are the ones I trusted, dry runs included
- **Dry runs**: one that declares it writes is passed `--dry-run`; one that doesn't runs as it is
- **What it runs with**: the secrets it declares, as environment variables (signed in with `auth login <program>`), a fixed PATH, my home folder, the desktop session's display (so it can open an app), and its own data folder as its working folder; nothing else from the app's environment
- **Its outcome**: exit code 0 is success; a one-line summary it writes to the file named by `$PA_RESULT` is the run's reason; what it prints is kept with the run, up to a limit
- **Never in the run queue**: programs don't wait for LLM routines' runs, and aren't held back by the usage limit
- **Ending it**: stopping it, its timeout or the scheduler stopping ends it and everything it started
- **Sleep mid-run**: one that doesn't write runs again once after the computer slept during it; one that writes never does

## Hooks
- **A hook is a prompt or a program run on events, not on a schedule**: its action is a Dotprompt file (an LLM hook) or an executable with a description file (`hook.yaml`, a program hook), and its trigger says which watcher's events it acts on, of which type. A hook has no schedule, and a routine no trigger but its schedule
- **Watchers** are programs the app runs to learn that something happened: an executable with a description file (`watcher.yaml`), polled every interval (given where it left off, it prints what's new) or streaming (one long-lived process printing events as they happen, started again when it ends). Several hooks can listen to one watcher; prompts can't be watchers
- **Events**: each has an ID, a type, when it happened and its data. A watcher's event is acted on once, even when it's printed again or the app restarts
- **Kept apart, run alike**: hooks and watchers live in their own folders, with their own commands (`hooks`, `watchers`), and never show among routines, not even with `--kind all`. A hook's runs share everything around a run with routines: trust, the run queue for an LLM hook (a program hook skips it), keeping the computer awake, waiting for a full wake, stop, pause, timeout, run status, history, notifications and the status dot
- **One name space**: names are unique across routines, programs, hooks and watchers; the commands that take a name find a hook by it (`stop`, `runs`, `routines pause`, `resume`, `trust`), and the routine-only ones (run, show, check) say it's a hook
- **Adding one**: from its file's path, in a terminal, an LLM hook validated as a routine is (its servers must exist and offer its tools, when they can be asked); a program hook's executable, and a watcher's, are copied with it, and the copy is what runs. A hook starts off and untrusted. Deleting one removes its copy, settings, trust and secrets, and keeps its run history
- **Trust**: a hook runs only while its action and its trigger are what I trusted, a program hook's dry runs included; a watcher runs only while its executable is the one I trusted. Trusting a hook trusts its watcher too, while it isn't trusted: one review shows both, and one yes covers both. Trusting an LLM hook that can write warns that it acts on outside data
- **Trying one**: there's no run now for a hook; it fires with a sample event (a file or stdin), as a dry run or for real
- **What an action gets**: an LLM hook's prompt gets the event, or every event that waited for it when it takes them in batches, each text from the watcher marked as outside data; a program hook gets its event as JSON on stdin, and the desktop session's display, so it can open an app
- **Sleep**: an event older than the hook's staleness is dropped; a watcher isn't polled while a Mac is in a maintenance wake, and a poll due while it slept runs as it wakes
- **Floods and loops**: a hook starts at most its rate of runs an hour, and an event past it is dropped; one due to run more than three times its rate in an hour is paused, with a run saying why
- **Pause and resume**: a hook can be turned off and on; pausing a watcher stops it, and the hooks that are on and listen to it show that as their problem until it's resumed

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

## Permissions
- **What the OS must allow, known before the first run**: what runs code declares the permissions it needs: a plugin (a plugin folder, a server added by URL or command, a plugin compiled in), a program, a program hook and a watcher. The set is macOS's: input monitoring, accessibility, screen recording, notifications, full disk access, calendars, contacts, reminders, microphone, camera and automation; any other is refused. The notify plugin declares notifications
- **Prompts inherit theirs**: a routine or an LLM hook declares none; it needs those of the plugins it uses. A hook needs its action's and its watcher's
- **Trust covers them**: a review lists every permission it needs, what needs each and its state; one added later needs a new review
- **Checked by the scheduler, as the app**: every permission, on each of its passes; the CLI, the MCP server and the frontends show what it found, and "not checked yet" before it has, never granted
- **One that isn't granted is a problem from the start**: a routine, program or hook that needs it doesn't run and isn't healthy, its problem naming the permission and where it's turned on; a watcher isn't started while one it declares isn't. One that can't be checked (full disk access and automation, which macOS has no check for) or isn't checked on this OS is never taken for missing
- **Asking for one**: `permissions request <permission>` has the scheduler ask the OS, so macOS's prompt names the app (macOS asks once; after that, and for one an app can't ask for, it says where in System Settings it's turned on, with the address that opens it); `permissions list` shows what's needed, by what, and its state
- **On Linux** input monitoring is read access to `/dev/input`; the others aren't checked

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
- **Health check** (`doctor`): explains a red indicator, hooks' too, with a fix for each problem
- **Routine check**: whether a routine would run (its file, settings, servers connecting, every allowed tool existing, and its model's provider set up and, for my own server, answering with the model), without calling the model

## Notifications
- **From routines** (the notify plugin): a routine can send me a short desktop notification, a title and a message, shown by the plugin itself through the OS (macOS, as the app; Linux, through the desktop's notification service), not by the menu bar app. An **urgent** one breaks through Focus/Do Not Disturb where the OS allows it and stays until dismissed; normal ones are quiet and fade. Clicking one does nothing. It has no settings, app-wide or per routine
- **The app's own**: a desktop notification when a run (a routine's or a hook's) fails, needs a sign-in, is missed or skipped, saying which routine or hook, when and why (can be turned off), and when an update installs or fails, shown by the menu bar app on macOS and the tray on Linux; clicking one opens the app: on the run it's about, in its routine's or hook's window, or (an update's) the app's Routines window. They don't depend on the notify plugin
- **On Linux** clicking one opens its run's report, or (an update's) the Routines window

## Status indicator
- **Icon** in the macOS menu bar, or the Linux tray, with a status dot:
  - **Green**: every enabled routine is fine. A routine with no runs yet is fine while its next run is ahead
  - **Red**: something needs me: a routine failed, was missed or skipped, needs a sign-in, can't be scheduled (a permission it needs not granted among the reasons), didn't start at its time or is stuck; a hook that's on failed, was missed, was paused past its rate, or can't run (its watcher missing, untrusted, paused or failing); setup isn't finished; or the scheduler isn't responding
  - **Grey**: no routines or hooks are enabled
  - **Pulsing**: a routine is running (whatever the colour); in the Linux tray the dot dims and brightens
- Updates within seconds of anything changing

## UI
Managing the app is possible entirely from macOS's menu bar app (automator-ui), without a terminal. The CLI does all of it too; the UI is another way in, not a replacement.
- **One indicator**: the menu bar app runs once; a second copy (opened beside the running one, e.g. by macOS after a permission is granted) quits at once
- **Menu** (the menu bar app's and the Linux tray's): the version; the status in words (each reason the dot is red, opening the run it's about, or the routine's or hook's window, on its review when it needs turning on); Finish Setup… while setup has steps left; **Routines ›** (all routines, then each with its status); **Hooks ›** while there are hooks (all hooks, then each with its status); updates; Settings; Quit
- **Quit** stops the app: the menu bar app or tray, and the scheduler (no routine runs on its schedule until I open the app again); both start again at the next login. When the scheduler can't be stopped, it says why and quits anyway
- **Opening the app** starts the scheduler if it isn't running, so opening it after a Quit brings everything back; opening it while it runs opens the Setup window while setup has steps left, else the Settings window
- **Turning one on**: a routine, program or hook that's off or not trusted is turned on with two clicks: **Turn On…** (in its window, beside it in the Routines and Hooks windows, from the menu or a reason) opens one review, and one confirm trusts it (a hook's watcher with it) and turns it on. The review says in a line what it does, warns first, offers granting each permission the OS hasn't allowed yet (the app asks the OS; while it's still not allowed, its settings page opens, saying what to switch on for the app; the row turns allowed by itself once the scheduler has checked) and signing in to whatever it still needs, says plainly it won't run until they're allowed (turning on is still possible), and keeps what it can reach or runs folded underneath; when the core refuses, it says why there. While it's on, the one action is Pause
- **Permissions in the windows**: a routine's, program's, hook's or watcher's window shows the permissions it needs with their state, a banner with Grant… while one isn't allowed (a reason about it opens the window there); ones that can't be checked or aren't on this OS are never shown as needed
- **Routines window**: every routine, LLM routines and programs together, with its health, schedule in words, last run and status
- **A routine's window**:
  - status: last run (when, outcome, reason), next run, why it can't be scheduled
  - its schedule in plain words (e.g. "At 09:20, Monday through Friday"), edited with a days-and-time picker or a custom cron
  - what it can reach (trusting it is turning it on, above)
  - its runs: what each did, its tool calls and writes, and its output
  - controls: run now or dry run, stop, pause/resume, delete
  - its memories (see, add, remove) and its own settings
  - for a program, the same window with only these differences:
    - what it runs, in place of what it can reach: its executable (its SHA-256, copyable), whether it writes, its arguments, and its secrets, each with its variable and whether it's stored; signing in to the ones that aren't
    - what a dry run does with it, beside the controls
    - each run's exit code and output, saying when the output was cut down
    - its description file, read-only, with its description and data folder
    - deleting it says its run history and data folder are kept
    - no memories or model
- **Hooks window**: every hook, LLM hooks and program hooks together, with its health, what it acts on in words, whether it's on, and its last run; and every watcher: how it runs, whether it's trusted or paused, the hooks listening, and why it's failing
- **A hook's window**: a routine's (a program's, for a program hook), with only these differences:
  - fire with a sample event (written as JSON), as a dry run or for real, in place of run now; pause and resume turn it off and on; no schedule or next run
  - what it acts on: its watcher (its state, opening its window), the event type, batches, staleness and rate
  - its review covers its watcher; when it runs is a line (its watcher, the event type), with the rest folded
  - each run's events (type, time, data), shown as plain text
  - memories for an LLM hook only
- **A watcher's window**: how it runs and last went, why it's failing, the hooks listening, what it runs with signing in to its secrets and trusting it alone (rarely needed), its file (read-only); pause, resume and delete
- **Setup window**: every setup step with a button for each
- **Settings window**: start at login, LLM usage, and every app-wide setting with a control for its type
- **Updates**: see whether a newer release exists, and install it
- **Start at login**: on or off
- **Linux tray**: the same menu, updates, notifications and windows (Setup, Settings, Routines, a routine's, Hooks, a hook's, a watcher's, Updating); a routine's schedule is edited as its cron setting, with no days-and-time picker. Installing an update is confirmed in a notification (where notifications have no buttons, in a window that asks). With no display to show a window on, or without the windows app, a window is its CLI command in my terminal (Setup: `setup`; Settings: `settings list --scope app`; Routines: `routines list --kind all`; a routine: `routines show <routine>`; Hooks: `hooks list`; a hook: `hooks show <hook>`; a watcher: `watchers show <watcher>`; Updating: `update progress`; asking before an update: `update --ask`)

## CLI
- **Everything is a command**: every feature, including what the UI does, with `--help` on each
- **Settings**: one generic `settings list/get/set/reset` surface reads and writes any setting, app-wide or per routine; values are checked before they're saved and apply without a restart
- **Paths**: `paths` shows where the app keeps its config, routines, programs, hooks, watchers, run status, history, notifications, templates and the frontends' log
- **Guide**: `guide` explains, from the app itself, how to write a routine (`guide routines`) and how to add a plugin or write my own, with the permissions one can declare (`guide plugins`), how to write a program (`guide programs`) and how to write a hook and a watcher (`guide hooks`), with examples that work, as text or `--json`; `routines create`, `routines update`, `hooks add`, `watchers add` and `plugins add` point to it
