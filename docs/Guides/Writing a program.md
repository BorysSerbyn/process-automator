Back to [[Home]]. See also [[Guides/Routines|Routines]], [[Spec/Requirements/Functional/Functional Requirements#Programs|Programs]].

A program is a routine that runs an executable instead of a model: for work that needs no judgement, done the same way every time. It's scheduled, run, stopped, paused and trusted like any routine, and shows up beside them. The reference is in the app, so it matches the version installed:

```sh
procauto guide programs
```

It has an example `program.yaml`, every key, what a program gets (its exit code, `$PA_RESULT`, notifying you through `$PA_NOTIFY`, dry runs, its environment, its output) and the steps once it's written. What follows is what the guide leaves out.

## Building it
Build it for the machine it runs on: a Go program for an Apple silicon Mac is `GOOS=darwin GOARCH=arm64 go build`. Anything it calls must be named by its full path or be on the guide's fixed PATH: it doesn't get your shell's.

## Permissions
If it needs something macOS guards (the screen, Calendars, Accessibility), list it in `program.yaml`'s `permissions`. Until each is granted it doesn't run and says which, with where it's turned on: `procauto permissions request <permission>` asks macOS as the app. Trusting it covers them: adding one later needs a new review.

## Changing it
The executable and `program.yaml` are copied into the app's own folder, and that copy is what runs. After rebuilding, `procauto routines update <name> --from <folder>/program.yaml`, then trust it again: a new executable, or a changed `program.yaml`, needs a new review. Claude can list, run and stop programs, but only you add or trust one.
