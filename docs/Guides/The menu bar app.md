Back to [[Home]]. See also [[Guides/Routines|Routines]], [[Guides/Writing a hook|Writing a hook]], [[Guides/Settings|Settings]], [[Guides/Troubleshooting|Troubleshooting]].

The menu bar app (automator-ui, macOS) starts at login. On Linux the tray takes its place: see [[#The Linux tray|The Linux tray]]. It's an hourglass in the menu bar with a status dot; everything it does is also a `procauto` command.

**Quit** stops the app: the menu bar app and the scheduler, so no routine runs until you open it again. Both start again at your next login, as before. **Opening the app** (**Process Automator** in `~/Applications`, Spotlight or Launchpad) starts the scheduler if it isn't running; opening it while it runs opens **Setup** while setup has steps left, else **Settings**. In a terminal, `procauto login-items stop` and `procauto login-items start` do the same.

## The dot
| Dot | Means |
|---|---|
| Green | Every enabled routine is fine |
| Green, pulsing | A routine is running |
| Grey | No routines are enabled |
| Red | Something needs you: a routine failed, was missed or skipped, needs a sign-in or can't be scheduled; setup isn't finished; or the scheduler isn't responding |

## The menu
- The version, then the status: "All good", or **why it's red**, one line per reason. Hover a reason for the detail; click it to open the run it's about (one about a hook opens the hook's window). [[Guides/Troubleshooting|Troubleshooting]] lists the reasons and their fixes
- **Finish Setup…** while setup has steps left: opens the Setup window
- **Routines ›**: **All Routines…**, then each routine with its status ("Next: tomorrow 09:20", "Paused", "Running since 09:20", "digest failed"). Click one to open its window
- **Hooks ›**, while there are hooks: **All Hooks…**, then each hook with its status ("Waiting for events", "Running since 09:30", "open-meet can't run"). Click one to open its window. Hooks never show under Routines ›
- **Update to X…** when a newer release exists (or **Check for Updates**); **Updating to X…** while one installs, which opens the Updating window: what it's doing (downloading, checking it's genuine, installing, or building from the checkout, which takes a few minutes) and the latest line of its output. Close it any time: the update carries on and the app restarts when it's done. If it fails, the window says why
- **Settings**, **Quit** (the scheduler stops too)

## Routines window
Every routine with its health, schedule in words, last run and status. Its toolbar opens **Setup** and **Settings**. Click a routine to open its window.

## A routine's window
- **Controls**: **Run Now** (its arrow offers **Dry Run**), **Stop** while it runs or waits its turn, **Pause** / **Resume** (whether it runs on its schedule), and **Delete…** under the `…` menu (its file, settings, trust and memories go; its run history stays)
- **Not trusted** banner, when it isn't: **Review…** shows what it can reach, with a **Trust** button
- **Overview**: last and next run, why it can't be scheduled, its schedule with a days-and-time picker or custom cron (applied on **Save**), and what it can reach (tinted tools write)
- **Runs**: each run with its outcome, final text, writes and every tool call; **Open Report** opens a readable report
- **Memories**: what this routine is told to remember; add and remove them
- **Settings**: its own settings (timeout, effort, folder, parameters, …)

A program's window (see [[Guides/Writing a program|Writing a program]]) is the same, but:
- **Dry Run** says what it does with this program, under it in the arrow's menu: one that writes is passed `--dry-run`; one that doesn't runs as it is. Either way it needs trusting first
- **What it runs**, in place of what it can reach: the app's copy of its executable and its SHA-256 (the copy button copies all of it), whether it writes, its arguments, and its secrets, each with the variable it's passed as and whether it's stored. **Sign In…**, while one isn't, opens your browser to enter them
- **Runs**: each run's summary, exit code, whether it was a dry run, when it finished, and what it printed; past a limit, only its start and end are kept, and it says so
- **Settings**: fewer settings of its own (no model, effort or folder); then its description file, read-only (`procauto routines update` changes it), with its description and data folder, where it works
- No **Memories**; **Delete…** removes its copy, settings, trust and secrets, and keeps its run history and data folder

What each of these means is in [[Guides/Routines|Routines]].

## Hooks window
Every hook, prompts and programs together, with its health, what it acts on ("calendar's meeting.started events"), whether it's off or not trusted, its last run and status; then every **watcher**: how it runs (polled every 30s, or streaming), whether it's trusted or paused, the hooks listening, and why it's failing. Click a hook or a watcher to open its window. Hooks and watchers are added from a terminal ([[Guides/Writing a hook|Writing a hook]]).

## A hook's window
A routine's window (or a program's, for a program hook), but:
- **Fire…** in place of Run Now: one event as JSON, as its watcher would print it (it starts with the type the hook acts on), with **Dry Run** and **Fire**, then how the run went. A program hook needs trusting even for a dry run; an LLM hook's dry run doesn't. A real fire shows in **Runs**
- **Pause** / **Resume** turn it off and on; no schedule and no next run
- **What it acts on**: its watcher (trusted, paused, failing; click it to open it), the event type, whether it takes events in batches, its staleness and its rate
- **What it can reach** (or **What it runs**) also shows what it acts on, which trusting it covers too, and any warning first (an LLM hook that can write acts on outside data). **Turn On** trusts a hook that's off and turns it on. While its watcher isn't trusted, a banner says the hook hears nothing until it is, with **Review <watcher>…**
- **Runs**: each run's trigger (`event`, or `manual` when fired) and the events it ran for: type, time, watcher and data, shown as plain text
- **Memories** for an LLM hook only; **Settings** has its own (on, staleness, timeout, rate; an LLM hook's effort, folder, …) and its file, read-only
- **Delete…** removes its copy, settings, trust and secrets, and keeps its run history

## A watcher's window
How it runs (its interval and timeout, when polled), when it last polled or started and last printed an event, and why it's failing; the hooks listening (click one to open it); what it runs (its executable and SHA-256, arguments, secrets with **Sign In…**), with **Trust** while it isn't trusted; and its description file, read-only. **Pause** stops it (the hooks listening show that as their problem); **Delete…** keeps the hooks listening, which then hear nothing.

## Setup window
Setup's steps, grouped: **Templates** (tick templates, fill in their parameters, **Install Selected**), **Model backend** (pick one; **Enter API Key…** for `claude-api`; on `local`, its server and model appear under the choice: Enter keeps Ollama on this machine, then type the model, e.g. `qwen3:8b`), **Plan usage** (**Set Up**), **Claude Code** (connected as soon as the window shows it; **Connect** tries again), **Routines to fix** (**Open <routine>…**), **Sign-ins** (**Sign In…** opens your browser), **Turn on routines** (review what each can reach, then **Turn On**: it's trusted and runs on its schedule). **Check Again** re-reads where setup stands. See [[Guides/Getting started#Set it up|Set it up]].

## Settings window
**Start at login**, the **LLM usage** card, then every app-wide setting as a tree (`llm` › `budget` › …), each with a control for its type and **Reset** to go back to the default. Changes apply at once. See [[Guides/Settings|Settings]].

## Notifications
The menu bar app shows the app's own desktop notifications: runs that **failed**, **need a sign-in**, were **missed** or were **skipped** (turn these off with `runs.failure-notifications`), and update results. Clicking one opens the app: a run's opens its routine's (or hook's) window on that run; an update's opens the Routines window.

What routines tell you with `notify_me` isn't the menu bar app's: the `notify` plugin shows it itself, also as the app (see [[Guides/Writing a routine|Writing a routine]]).

macOS asks once whether the app may show notifications (for both); until you allow it, the app's own wait.

## The Linux tray
In a Linux desktop session the tray (automator-tray) shows the same icon and dot in the system tray: the dot dims and brightens while a routine runs. Its menu is the menu bar app's, and so are its notifications; a click on one about a run opens the run's report, and one about an update the Routines window. It starts at login. **Quit** stops it and the scheduler, as on macOS, until you open the app again or log in again. The app is in your desktop's app menu (search for **Process Automator**): opening it starts the scheduler and brings the tray back, or, while the tray runs, opens Setup or Settings as on macOS. The tray's log is `frontend.log` in the data folder (see [[Guides/Troubleshooting#Logs|Logs]]).

Its windows are the menu bar app's: [[#Routines window]], [[#A routine's window]], [[#Hooks window]], [[#A hook's window]], [[#A watcher's window]], [[#Setup window]], [[#Settings window]] and Updating; opening one again brings it to the front. A routine's window has the same controls and tabs (**Overview**, **Runs**, **Memories**, **Settings**), but no days-and-time picker: its schedule is its `schedule` setting, in cron, under **Settings**. A program's is the menu bar app's too; what **Dry Run** does with it is written under the controls. A paused routine that isn't trusted has **Turn On** in its review: it's trusted and resumed together. A hook's **Fire…** opens its sample event above the tabs, and a reason about a hook with no run opens its window.

A window opens in your terminal instead (`$TERMINAL`, else the first of `xdg-terminal-exec`, `x-terminal-emulator`, `ptyxis`, `kgx`, `gnome-terminal`, `konsole`, `xfce4-terminal` and `xterm`, kept open until you press Enter) when it can't open: no display, no OpenGL, or a package built without the windows app. The tray's log says why:

| Window | Runs |
|---|---|
| Setup | `procauto setup` |
| Settings | `procauto settings list --scope app` |
| Routines | `procauto routines list` |
| A routine | `procauto routines show <routine>` |
| Hooks | `procauto hooks list` |
| A hook | `procauto hooks show <hook>` |
| A watcher | `procauto watchers show <watcher>` |
| Updating | `procauto update progress` |

**Update to X…** asks in a notification, with **Update** and **Later** (where notifications have no buttons, in a window). On GNOME the tray needs the AppIndicator extension (Ubuntu has it on); see [[Guides/Getting started#On Linux|On Linux]].
