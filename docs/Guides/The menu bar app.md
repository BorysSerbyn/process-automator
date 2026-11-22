Back to [[Home]]. See also [[Guides/Routines|Routines]], [[Guides/Settings|Settings]], [[Guides/Troubleshooting|Troubleshooting]].

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
- The version, then the status: "All good", or **why it's red**, one line per reason. Hover a reason for the detail; click it to open the run it's about. [[Guides/Troubleshooting|Troubleshooting]] lists the reasons and their fixes
- **Finish Setup…** while setup has steps left: opens the Setup window
- **Routines ›**: **All Routines…**, then each routine with its status ("Next: tomorrow 09:20", "Paused", "Running since 09:20", "digest failed"). Click one to open its window
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

What each of these means is in [[Guides/Routines|Routines]].

## Setup window
Setup's steps, grouped: **Templates** (tick templates, fill in their parameters, **Install Selected**), **Model backend** (pick one; **Enter API Key…** for `claude-api`; on `local`, its server and model appear under the choice: Enter keeps Ollama on this machine, then type the model, e.g. `qwen3:8b`), **Plan usage** (**Set Up**), **Claude Code** (connected as soon as the window shows it; **Connect** tries again), **Routines to fix** (**Open <routine>…**), **Sign-ins** (**Sign In…** opens your browser), **Turn on routines** (review what each can reach, then **Turn On**: it's trusted and runs on its schedule). **Check Again** re-reads where setup stands. See [[Guides/Getting started#Set it up|Set it up]].

## Settings window
**Start at login**, the **LLM usage** card, then every app-wide setting as a tree (`llm` › `budget` › …), each with a control for its type and **Reset** to go back to the default. Changes apply at once. See [[Guides/Settings|Settings]].

## Notifications
The menu bar app shows the app's own desktop notifications: runs that **failed**, **need a sign-in**, were **missed** or were **skipped** (turn these off with `runs.failure-notifications`), and update results. Clicking one opens the app: a run's opens its routine's window on that run; an update's opens the Routines window.

What routines tell you with `notify_me` isn't the menu bar app's: the `notify` plugin shows it itself, also as the app (see [[Guides/Writing a routine|Writing a routine]]).

macOS asks once whether the app may show notifications (for both); until you allow it, the app's own wait.

## The Linux tray
In a Linux desktop session the tray (automator-tray) shows the same icon and dot in the system tray: the dot dims and brightens while a routine runs. Its menu is the menu bar app's, and so are its notifications; a click on one about a run opens the run's report, and one about an update the routines list. It starts at login. **Quit** stops it and the scheduler, as on macOS, until you open the app again or log in again. The app is in your desktop's app menu (search for **Process Automator**): opening it starts the scheduler and brings the tray back, or, while the tray runs, opens Setup or Settings as on macOS. The tray's log is `frontend.log` in the data folder (see [[Guides/Troubleshooting#Logs|Logs]]).

**Finish Setup…** and **Settings** open the same Setup and Settings windows as on macOS ([[#Setup window]], [[#Settings window]]); opening one again brings it to the front. The other windows aren't on Linux yet: those menu items run their `procauto` command in your terminal (`$TERMINAL`, else the first of `xdg-terminal-exec`, `x-terminal-emulator`, `ptyxis`, `kgx`, `gnome-terminal`, `konsole`, `xfce4-terminal` and `xterm`), which stays open until you press Enter:

| Menu item | Runs |
|---|---|
| **All Routines…** | `procauto routines list` |
| A routine | `procauto routines show <routine>` |
| **Updating to X…** | `procauto update progress` |

Setup and Settings open in a terminal too (`procauto setup`, `procauto settings list --scope app`) when their window can't open: no display, no OpenGL, or a package built without the windows app.

**Update to X…** asks in a notification, with **Update** and **Later** (where notifications have no buttons, in your terminal: `procauto update --ask`). On GNOME the tray needs the AppIndicator extension (Ubuntu has it on); see [[Guides/Getting started#On Linux|On Linux]].
