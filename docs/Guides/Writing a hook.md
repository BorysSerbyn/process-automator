Back to [[Home]]. See also [[Guides/Writing a program|Writing a program]], [[Spec/Requirements/Functional/Functional Requirements#Hooks|Hooks]].

A hook is a prompt or a program run when something happens, instead of on a schedule: open Google Meet when a meeting starts, look at new email as it comes. What happens comes from a watcher, a program the app runs that prints events; the hook says whose events it acts on and what it does. The reference is in the app, so it matches the version installed:

```sh
procauto guide hooks
```

It has an example watcher, a program hook and an LLM hook, every key, the event format, what each kind of hook gets, and the steps once they're written. What follows is what the guide leaves out.

## Writing a watcher
Run it by hand first: a poll watcher is a program that reads `$PA_CURSOR`, prints JSON lines and exits, so `PA_CURSOR= ./calendar-watch` shows what the app would get. Give each event an ID that stays the same when it's printed again (the calendar event's ID with its start time, say): that's how the app acts on it once. Build it for the machine it runs on, as a program, and call anything it needs by its full path.

## Trying a hook
There's no run now for a hook, since it runs for an event: `hooks fire` gives it a sample, a line as its watcher would print it, saved in a file. With `--dry-run`, an LLM hook's writes are intercepted and a program hook that writes is passed `--dry-run`.

## Opening apps
On macOS a hook's program can open an app as from a terminal (`open -a`, `open <url>`). On Linux it gets the desktop session's display variables when the scheduler has them; under systemd that's when the session hands them to systemd's user manager, as GNOME and KDE do. If an app doesn't open, add `systemctl --user import-environment DISPLAY WAYLAND_DISPLAY XAUTHORITY` to the session's startup, then restart the scheduler (`procauto login-items stop`, then `start`).

## Changing them
The files are copied into the app's own folders, and the copies are what run. After a change, `procauto hooks update <name> --from <file>` or `procauto watchers update <name> --from <folder>/watcher.yaml`, then trust it again: another action, watcher or event type for a hook, or another executable for a watcher, needs a new review. Claude can list, fire, pause and stop hooks and watchers, but only you add or trust one.
