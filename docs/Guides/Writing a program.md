Back to [[Home]]. See also [[Guides/Routines|Routines]], [[Spec/Requirements/Functional/Functional Requirements#Programs|Programs]].

A program is a routine that runs an executable instead of a model: for work that needs no judgement, done the same way every time. It's scheduled, run, stopped, paused and trusted like any routine, and shows up beside them.

## Write it
Any executable will do; a Go binary is the usual one. What the app gives it:
- **Success** is exit code 0. Write one line saying what it did (or why it failed) to the file named by `$PA_RESULT`: run history shows it
- **Dry runs**: with `writes: true` it's passed `--dry-run`, and must then change nothing; with `writes: false` a dry run runs it as it is
- **Environment**: a fixed PATH (the system's folders, and Homebrew's on macOS), `HOME`, `TMPDIR`, its secrets, and `PA_TRIGGER` (`schedule`, `manual` or `backfill`), `PA_FOR` (the time it's for) and `PA_RUN_ID`. It works in a folder of its own that's kept between runs
- **Output**: what it prints goes to the run's log and history, up to 64 KiB
- Stopping it, or its timeout, ends it and everything it started

## Describe it
Put a `program.yaml` beside the executable:

```yaml
description: Backs up my notes to the NAS
executable: ./backup       # relative to this file
writes: true               # required: can it change anything?
args: [--quiet]
schedule: "0 2 * * *"      # optional, cron, local time
timeout: 10m
secrets:
  - name: nas-token        # passed as $NAS_TOKEN (or name an env)
```

## Add it, trust it, turn it on
```sh
procauto routines create backup --kind program --from ./backup/program.yaml
procauto auth login backup                        # its secrets, if it has any
procauto routines trust backup --resume           # review, trust and turn it on
procauto run backup --dry-run                     # once trusted
procauto routines list --kind all                 # programs with your routines
```

The executable and `program.yaml` are copied into the app's own folder, and that copy is what runs. After rebuilding, `procauto routines update backup --from ./backup/program.yaml`, then trust it again: a new executable, or a changed `program.yaml`, needs a new review. A program runs only while trusted, dry runs too. Claude can list, run and stop programs, but only you add or trust one.
