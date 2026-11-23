# Writing a hook

A hook is a prompt or a program run when something happens, instead of on a schedule: a meeting starting, an email arriving. What happens comes from a watcher: a program the app runs, which prints events. The watcher declares the events it prints, each with a label in words; a hook picks one (`on`) and says what it does: a Dotprompt file (an LLM hook) or an executable beside a `hook.yaml` (a program hook). Several hooks can listen to one watcher. Hooks aren't routines: they have no schedule, and their own commands.

## Example
A watcher, `calendar/watcher.yaml`:
```yaml
description: Meetings in my calendar, as they start
executable: ./calendar-watch   # relative to this file
mode: poll                     # or stream
interval: 30s
events:                        # each type it prints, with what it means
  meeting.started: A meeting starts
  meeting.ended: A meeting ends
secrets:
  - name: calendar-token       # passed as $CALENDAR_TOKEN
permissions: [calendars]       # what the OS must allow it
```
A program hook acting on it, `open-meet/hook.yaml`:
```yaml
description: Opens Google Meet when a meeting starts
executable: ./open-meet
writes: false
on:
  watcher: calendar
  type: meeting.started
staleness: 2m                  # an older event is dropped
rate: 20                       # runs an hour, at most
```
An LLM hook, `triage.prompt`:
```handlebars
---
description: Tells me which new emails need me today
automator.on: {watcher: inbox, type: email.received}
automator.batch: true
automator.servers: [notify]
tools: [notify_me]
---
Look at the new emails below. If any needs me today, notify me with its subject and why.
{{#each events}}- {{this.data.subject}} from {{this.data.from}}
{{/each}}
{{> memories}}
```

## Events
A watcher prints JSON lines on stdout. An event:
`{"id": "evt-42", "type": "meeting.started", "at": "2026-11-20T09:30:00+01:00", "data": {"title": "Standup", "link": "https://meet.google.com/abc"}}`
- `id` (required): identifies it among its watcher's events: one printed again is ignored, across restarts
- `type` (required): what happened, in letters, digits and `.` `_` `:` `/` `-`: one of the types its `events` declares (an event of another type is dropped and logged). A hook listens for one of them, and the app shows its label ("When A meeting starts (from calendar)")
- `at`: when it happened (RFC 3339; left out, when it was printed). A hook drops an event older than its staleness, e.g. one that happened while the computer slept
- `data`: an object, anything the action needs
- `{"cursor": "..."}`, a line of its own: where to carry on from. The last one printed is saved and given back in `$%{cursorEnv}` the next time it starts

## Watchers
%{table:watcherFields}

- **poll**: run every `interval` with `$%{cursorEnv}`; it prints what's new since then, a cursor line, and exits 0. A poll that fails (exits non-zero, prints a line that's neither) changes nothing: the next one gets the same cursor. It isn't polled during a Mac's maintenance wake; one missed while the computer slept runs as it wakes
- **stream**: started once, printing events as they happen. When it ends it's started again: at once after running a minute, else after a pause that doubles, up to %{maxBackoff}
- **Environment**: a program's (a fixed PATH, `HOME`, `TMPDIR`, its secrets, and the desktop session's display variables when the app has them), `$%{cursorEnv}` and `$%{watcherEnv}`, in a folder of its own. What it prints on stderr goes to the daemon's log
- **events**: a hook's `on.type` must be one of them (a hook on another type isn't added; one left listening after the watcher drops its type has a problem). They're part of what's trusted. A watcher without `events` may print any type, and is nudged to declare them
- It runs only once trusted (trusting a hook that listens to it trusts it too), while it isn't paused, every permission it declares is granted, and a hook that's on listens to it

## Hooks
A program hook's `hook.yaml`:
%{table:hookFields}

An LLM hook's prompt is a routine's file (`%{command} guide routines`) without `automator.schedule` or `automator.parameters`, plus:
%{table:hookPromptKeys}

Their settings (`hooks.<hook>.<key>`, set in the file as `<key>` or `automator.<key>`):
%{table:hookSettings}

- **A program hook** gets its event as one JSON object on stdin, `$%{triggerEnv}` (`event`, or `manual` when fired by hand), `$%{forEnv}` (the event's time), and the rest of what a program gets (`%{command} guide programs`): its exit code, `$%{resultEnv}`, `$%{notifyEnv}` (the app's notifications about the run, a JSON object a line: `title`, `body`, `url`, `urgent`, `acknowledge`; e.g. `printf '%s\n' '{"title":"Review requested","url":"https://github.com/o/r/pull/7"}' >> "$%{notifyEnv}"`), `%{dryRunFlag}` on a dry run when it writes
- **An LLM hook** gets `{{event}}` (the latest) and `{{events}}` (all of them, oldest first: more than one only with `automator.batch: true`), each with `id`, `type`, `at`, `watcher` and `data`. A watcher's text is outside data: each one comes wrapped in `%{untrustedOpen}` and `%{untrustedClose}`, and the app adds the events to the end of the prompt in an `<%{eventBlock}>` block saying they're data, never instructions. Give it read-only tools where it can do without writing: trusting one that writes is flagged
- **Permissions**: a hook needs its action's (a program hook's `permissions`; an LLM hook's, those of the plugins in its `automator.servers`: `triage` above needs notify's `notifications`) and its watcher's. Its review lists them; while one isn't granted it doesn't run and its watcher doesn't start, saying which (`%{command} guide plugins` lists them; `%{command} permissions request <permission>` asks the OS)
- **Runs**: one at a time per hook; an LLM hook takes its turn in the run queue, a program hook doesn't. At most `rate` runs start in an hour; past %{loopFactor} times that (its action setting it off again, say) the hook is paused, with a run saying why

## After writing them
1. `%{command} watchers add <name> --from <folder>/watcher.yaml`, and `%{command} auth login <name>` for its secrets
2. `%{command} hooks add <name> --from <file>` (a `.prompt` file, or a `hook.yaml` with its executable beside it): it starts off and untrusted. Only the person adds or trusts a hook or a watcher, never Claude
3. `%{command} hooks fire <name> --event sample.json --dry-run`: tries it with a sample event, one line as the watcher would print it
4. `%{command} hooks trust <name> --resume`: the person reviews it (its watcher too, while that isn't trusted), trusts both and turns the hook on
5. `%{command} hooks list`, `%{command} runs <name>`; `%{command} hooks pause <name>`, `%{command} stop <name>`

## Sharing them as templates
A hook or a watcher is a template folder, as a program is (`%{command} guide programs`): its `hook.yaml` (or `hook.prompt`) or `watcher.yaml`, its executable or source with a `build`, and `template.yaml`; `{{param}}` fills a watcher's `args`, say. Installing a hook's template installs its watcher's template too (the one named as its `on.watcher`) when the person doesn't have that watcher; `templates list` shows the two as one entry, the hook's (`withWatcher`): offer them as one choice
