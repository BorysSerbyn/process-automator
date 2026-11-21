Back to [[Home]]. See also [[Guides/Routines|Routines]], [[Guides/Settings|Settings]], [[Guides/Troubleshooting|Troubleshooting]].

The menu bar app (automator-ui, macOS) starts at login. It's an hourglass in the menu bar with a status dot; everything it does is also a `procauto` command. If you quit it, open **Process Automator** from `~/Applications` to bring it back.

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
- **Update to X…** when a newer release exists (or **Check for Updates**)
- **Settings**, **Quit**

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
Setup's steps, grouped: **Templates** (tick templates, fill in their parameters, **Install Selected**), **Model backend** (pick one; **Enter API Key…** for `api`), **Plan usage** (**Set Up**), **Claude Code** (connected as soon as the window shows it; **Connect** tries again), **Routines to fix** (**Open <routine>…**), **Sign-ins** (**Sign In…** opens your browser), **Turn on routines** (review what each can reach, then **Turn On**: it's trusted and runs on its schedule). **Check Again** re-reads where setup stands. See [[Guides/Getting started#Set it up|Set it up]].

## Settings window
**Start at login**, the **LLM usage** card, then every app-wide setting as a tree (`llm` › `budget` › …), each with a control for its type and **Reset** to go back to the default. Changes apply at once. See [[Guides/Settings|Settings]].

## Notifications
The app shows desktop notifications for:
- its own: runs that **failed**, **need a sign-in**, were **missed** or were **skipped** (turn these off with `runs.failure-notifications`), and update results. Clicking one shows the run it's about
- what routines tell you with `notify_me` (see [[Guides/Writing a routine#Notifying you|Notifying you]]); an **urgent** one breaks through Focus and stays until dismissed. Clicking one shows the run that sent it, unless the routine attached a file to open, or you changed that routine's click action (`routines.<name>.onNotificationClick`: `show-run`, `open <file, folder or URL>`, `run <shell command>` or `none`)

macOS asks once whether the app may show notifications; until you allow it, they wait. A `run` command gets `PROCAUTO_NOTIFICATION_ID`, `PROCAUTO_NOTIFICATION_TITLE`, `PROCAUTO_NOTIFICATION_BODY`, `PROCAUTO_ROUTINE` and `PROCAUTO_RUN` in its environment.
