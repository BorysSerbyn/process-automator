Back to [[Home]]. See also [[Guides/Writing a routine|Writing a routine]], [[Guides/The menu bar app|The menu bar app]].

A routine goes through the same steps whichever way you add it: **add it**, **try it** with a dry run, **trust it**, **turn it on**. A new routine starts paused and untrusted, so nothing runs for real until you've done the last two.

## Add your own
Write a routine file ([[Guides/Writing a routine|Writing a routine]]), then add it:

```sh
procauto routines create digest --from ./digest.prompt
procauto routines update digest --from ./digest.prompt   # replace it later; its settings are kept
```

Both validate the file first. You can also edit files in the routines folder directly (`procauto paths` shows where), or ask Claude to write one ([[Guides/Using it from Claude|Using it from Claude]]). `procauto routines delete <name>` removes a routine with its settings, trust and memories; its run history stays.

## Add one from a template
Templates are routines shared as files, grouped by tags (general to specific). The app ships none: they come from the repository's `templates/` folder when you or your team add some there, and a release carries whatever that folder holds.

```sh
procauto templates list
procauto templates install projects/acme/standup --set channel=C0123456789
```

- `--set <param>=<value>` gives a parameter its value (repeatable); a required one you leave out is asked for. `--as <name>` names the routine differently, e.g. to install one template twice with different parameters
- Templates under `projects/<project>/` are for that project's members; setup asks which projects you work on before offering them
- The template is copied into your routines folder, where it's yours to edit. Parameter values become settings (`routines.<name>.params.<param>`), so you can change them later (see [[Guides/Settings|Settings]])
- `templates list` shows only the ones for your OS; `--all` shows every one
- In the menu bar app: Setup window › **Templates**

Check which servers an installed routine uses (`procauto routines show <name>`), add any that are missing ([[Guides/Adding a plugin|Adding a plugin]]) and sign in to them ([[Guides/Plugins and sign-ins|Plugins and sign-ins]]).

## Try it
```sh
procauto routines check digest     # without calling the model: file, settings, servers connect, allowed tools exist
procauto run digest --dry-run      # a real run, except that nothing is posted or written
```

A dry run calls the model and every read-only tool for real, and intercepts every tool that writes: it prints what each write would have sent, then the routine's final reply. Dry runs need no trust and leave no run history. From the menu bar app, **Run Now** › **Dry Run** runs it in the background; its output goes to `start.log` in the data folder.

## Trust it
A routine runs for real only once you've trusted it with what it can reach: its servers, the tools it may call (split into writes and reads), the Claude Code built-in tools it may use, its plugin sections and its folder.

```sh
procauto routines trust digest     # shows what it can reach and what changed, then asks
```

In the menu bar app: the routine window's **Review…** › **Trust**, or Setup › **Trust**. Only you can trust a routine: Claude can't, through the MCP server.

**Trust is taken away** when any of what it can reach changes (a new server, tool, built-in tool, plugin section or folder). Its prompt, schedule, model and other settings don't affect it. An untrusted routine isn't scheduled (the dot turns red: "can't be scheduled"), and running it for real is refused, until you trust it again.

## Turn it on
```sh
procauto routines resume digest    # runs on its schedule
procauto routines pause digest     # doesn't; you can still run it by hand
```

In the menu bar app: **Pause** / **Resume** in its window. A routine without a schedule only runs by hand.

To change when it runs, use the schedule picker in its window, or set a cron (local time):

```sh
procauto schedule describe '20 9 * * 1-5'                       # "At 09:20, Monday through Friday", next times
procauto schedule build --days mon,wed,fri --at 08:30           # the cron for those days and time
procauto settings set routines.digest.schedule '0 17 * * 5'
```

If your computer was asleep or off at a scheduled time, the run starts when the scheduler is back, as long as it's no later than the routine's `staleness`; later than that it's recorded as missed.

## Run it now, stop it
```sh
procauto run digest                         # now, in this terminal; prints its reply
procauto start digest                       # now, in the background
procauto run digest --date 2026-11-16       # as of a past day (a backfill)
procauto stop digest                        # stops a run in progress or waiting its turn
```

In the menu bar app: **Run Now** and **Stop**. Running by hand ignores the schedule and whether the routine is paused, but needs trust. Runs take turns, one at a time, unless a routine is set to run in parallel; one waiting shows as "Waiting for its turn". A stopped run is recorded as stopped, not failed. A backfill computes `today` and `since` for that day: Monday's date covers Friday.

## Read what it did
```sh
procauto runs                                    # recent runs: when, trigger, outcome, why
procauto runs digest --since 7d
procauto runs show digest/2026-11-19T092022        # one run: inputs, tool calls, writes, tokens, final text
procauto runs show digest/2026-11-19T092022 --open # as a readable report
```

In the menu bar app: the routine window's **Runs** tab. Outcomes are `success`, `failed`, `needs-reauth`, `missed`, `skipped` (over the usage limit) and `stopped`. History is kept for `history.retention-days` (90 by default).

## Memories
Memories are short, lasting rules you give routines, e.g. "Leave out issues labelled wontfix". They're yours only: routines read them, never write them.

```sh
procauto memory add "Write dates as YYYY-MM-DD"                          # for every routine
procauto memory add "Leave out issues labelled wontfix" --routine digest  # for one routine
procauto memory list [--routine digest]
procauto memory edit <id> "<new text>"
procauto memory rm <id>
procauto memory compact [--routine digest]   # merges duplicates and outdated ones, after showing you
```

A routine gets them only where its prompt includes `{{> memories}}`. Each memories file is kept small (`memories.max-chars`); past it, `memory add` asks you to compact first. They're plain Markdown files in the data folder (`memories/_global.md`, `memories/<routine>.md`, one memory per `- ` line), which you can edit by hand. In the menu bar app, a routine window's **Memories** tab shows that routine's own.

Facts that must be applied exactly (e.g. which repository maps to which team) belong in the routine's prompt or parameters, not in memories.
