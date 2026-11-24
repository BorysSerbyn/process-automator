Back to [[Home]]. See also [[Spec/Requirements/Technical/Distribution|Technical: Distribution]], [[Guides/Getting started|Getting started]].

## Audience
- **Public**: anyone can install and update it, with no GitHub account
- **An app built on it chooses**: its releases can be public too, or private, for the people it invites, who sign in to GitHub to install and update it

## Install, update, uninstall
- **Installable without building from source**, on macOS and Linux
- **One command each** to install, update and uninstall, on both
- **Verified**: installing and updating only ever install a release verified as the one published, and nothing from it runs before that check passes
- **Updates move only to published releases**, never to unreleased commits; a development build newer than the latest release is left alone. `update --check` only reports
- **Updates say what's wrong**: when the releases can't be read (no network; for a private app, no GitHub access), updating says why and changes nothing
- **Updates show their progress**: while one installs, what it's doing (downloading, verifying, installing, or building, which takes minutes), so it never looks stuck; and how it ended if it failed
- **Updates keep everything**: my routines, settings, sign-ins and whether the app starts at login
- **Automatic updates, on by default** (existing installs too, unless I turned them off): a newer release installs by itself, with nothing for me to do, at a moment that doesn't disturb me: once it has been out an hour; never while a routine or hook runs or waits its turn, or a scheduled run is minutes away; never while the app's window, or its menu, is open, nor for a few minutes after; only once the computer has been awake a while, never in a maintenance wake. A notification says afterwards that it's updated to X, and clicking it shows what's new. Install updates automatically (Updates) or `update.auto` turns them off
- **A failed automatic update says so**, in a notification, and the version I had keeps working; the same release is tried again a day later, a newer one once it's been out an hour
- **Only what can be installed safely by itself is**: published releases with a download for this machine, never pre-releases or drafts, and never development builds (they move forward with `make install`), Homebrew formula installs (brew updates those) or a build from the source checkout; those are installed by hand
- **Checked every hour** by the scheduler (a private app's with the same GitHub sign-in as `update`), so the menu and Updates know of a newer release without asking GitHub; when the releases can't be read, Updates says why and nothing installs
- **Release notes**: Updates shows the installed version's notes and a newer release's, as their GitHub releases have them (`update notes`)
- **Uninstalling keeps my data**: config, routines, history, logs and secrets stay unless I ask for them to go too
- **Starts at login**: install sets the scheduler, and the menu bar app on macOS or the tray in Linux desktop sessions, to start at login and starts them now (the tray only in a desktop session: a headless machine runs without it)
- **Found where apps are**: on macOS the app is in `~/Applications`; on Linux install adds it to the desktop's app menu, and uninstall removes it
- **Seamless dev updates**: reinstalling a local build is one quick command; no re-granting permissions, no re-entering secrets, no logging out; running apps restart on the new build
- **Homebrew**: a private tap, for invited people (a cask on macOS, a formula on Linux)

## Versions
- **Every install is a known version**, following semantic versioning (major.minor.patch)
- **Shown** in the menu bar app's or tray's menu and by the CLI (`version`, `--version`)
- **Development builds are marked** as such (`0.1.0-dev+<commit>`), distinct from releases
- **Cutting a release is one command**

## First-run setup
- **Guided setup** (`setup` in a terminal, Finish Setup… in automator-ui and the Linux tray): optional templates (first asking which projects I work on, when any templates are made for a project), the model backend and its sign-in (on `local`, the server and which of its models to run), reading my Claude plan's usage when the default way can't, connecting Claude Code to the app's MCP server (on the `claude-code` backend), routines that can't run, sign-ins for the servers my routines use, and turning each routine on: trusting it with what it can reach and running it on its schedule, in one yes
- **Asks before each step**, except connecting Claude Code to the app's MCP server, which it does without asking; skips what's done, and can run again any time
- **Unfinished setup shows**: until the required steps are done, the status dot is red with "Finish Setup (N to do)…", opening setup
- **Usable without routines**: installing templates is optional

## Installing through an LLM
- **I can tell an LLM** (e.g. Claude Code) "find the process-automator repo and install it", and it installs the app by following instructions the repository keeps for LLMs
- **It asks rather than chooses**: it guides me through the same choices as the installer and setup (which templates, the model backend, sign-ins, turning routines on), one question at a time
