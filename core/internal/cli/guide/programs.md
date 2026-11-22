# Writing a program

A program is a routine that runs an executable instead of a model: for work that needs no judgement, done the same way every time. Any executable will do; a Go binary is the usual one. Beside it goes a description file, `program.yaml`.

## Example
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

## Keys
%{table:programFields}

## What it gets
- **Success** is exit code 0. One line saying what it did, or why it failed, goes in the file named by `$%{resultEnv}`: run history shows it. Without one, a failure's reason is its output's last line
- **Dry runs**: with `writes: true` it's passed `%{dryRunFlag}` and must change nothing; with `writes: false` a dry run runs it as it is
- **Environment**: a fixed PATH (`%{programPath}`), `HOME`, `TMPDIR`, its secrets, `$%{triggerEnv}` (`schedule`, `manual` or `backfill`), `$%{forEnv}` (the time it runs for) and `$%{runIDEnv}`. Call anything else by its full path. It works in a folder of its own, kept between runs
- **Output**: stdout and stderr go to the run's log and history, the first and last %{outputHalf} KiB when there's more
- Stopping it, or its timeout, ends it and everything it started

## After writing it
1. `%{command} routines create <name> --kind program --from <folder>/program.yaml`: copies it and its executable into the app; starts paused and untrusted. Only the person adds a program, never Claude
2. `%{command} auth login <name>` for its secrets, if it has any
3. `%{command} routines trust <name> --resume`: the person reviews, trusts and turns it on
4. `%{command} run <name> --dry-run`, once trusted
5. After rebuilding: `%{command} routines update <name> --from <folder>/program.yaml`, then trust it again
