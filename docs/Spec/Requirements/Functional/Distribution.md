Back to [[Home]]. See also [[Spec/Requirements/Technical/Distribution|Technical: Distribution]], [[Guides/Getting started|Getting started]].

## Audience
- **Private**: me and people I invite to the repository; nothing blocks opening it up later

## Install, update, uninstall
- **Installable without building from source**, on macOS and Linux
- **One command each** to install, update and uninstall, on both
- **Verified**: installing and updating only ever install a release verified as the one published, and nothing from it runs before that check passes
- **Updates move only to published releases**, never to unreleased commits; a development build newer than the latest release is left alone. `update --check` only reports
- **Updates say what's wrong**: when the releases can't be read (no GitHub access, no network), updating says why and changes nothing
- **Updates show their progress**: while one installs, what it's doing (downloading, verifying, installing, or building, which takes minutes), so it never looks stuck; and how it ended if it failed
- **Updates keep everything**: my routines, settings, sign-ins and whether the app starts at login
- **Uninstalling keeps my data**: config, routines, history, logs and secrets stay unless I ask for them to go too
- **Starts at login**: install sets the scheduler (and on macOS the menu bar app) to start at login and starts them now
- **Seamless dev updates**: reinstalling a local build is one quick command; no re-granting permissions, no re-entering secrets, no logging out; running apps restart on the new build
- **Homebrew**: a private tap, for invited people (a cask on macOS, a formula on Linux)

## Versions
- **Every install is a known version**, following semantic versioning (major.minor.patch)
- **Shown** in the menu bar menu and by the CLI (`version`, `--version`)
- **Development builds are marked** as such (`0.1.0-dev+<commit>`), distinct from releases
- **Cutting a release is one command**

## First-run setup
- **Guided setup** (`setup` in a terminal, Finish Setup… in automator-ui): optional templates (first asking which projects I work on, when any templates are made for a project), the model backend and its sign-in, reading my Claude plan's usage when the default way can't, connecting Claude Code to the app's MCP server (on the `claude-code` backend), routines that can't run, sign-ins for the servers my routines use, and turning each routine on: trusting it with what it can reach and running it on its schedule, in one yes
- **Asks before each step**, except connecting Claude Code to the app's MCP server, which it does without asking; skips what's done, and can run again any time
- **Unfinished setup shows**: until the required steps are done, the status dot is red with "Setup isn't finished"
- **Usable without routines**: installing templates is optional

## Installing through an LLM
- **I can tell an LLM** (e.g. Claude Code) "find the process-automator repo and install it", and it installs the app by following instructions the repository keeps for LLMs
- **It asks rather than chooses**: it guides me through the same choices as the installer and setup (which templates, the model backend, sign-ins, turning routines on), one question at a time
