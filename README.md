<p align="center">
  <img src="automator-ui/Resources/Logo/AppIcon-1024.png" alt="Process Automator logo: an hourglass drawn as one stroke running into a green status dot" width="160">
</p>

# process-automator

A lightweight blank canvas for scheduled LLM routines. You describe a task in a prompt file, choose when it runs, and it gets done in the background with the tools you allow it. On its own the app does nothing; your routines are what give it a purpose.

Tools come from MCP servers. The app has two of its own (`automator`, its read-only view of itself, and `notify`, which lets a routine notify you); any other MCP server, remote or local, is added with `procauto plugins add`.

Documentation lives in [`docs/`](docs/Home.md), which is an Obsidian vault.

## Install

macOS 14+ or Linux (x86-64, ARM64). The repository is private, so sign in to GitHub first (`gh auth login`, or set `GH_TOKEN`). Then, from a checkout:

```sh
./install.sh            # the latest release, checked and installed; then offers `procauto setup`
```

or without one:

```sh
gh api repos/BorysSerbyn/process-automator/contents/install.sh -H 'Accept: application/vnd.github.raw' | bash
```

It installs the app (macOS: `~/Applications/Process Automator.app`; Linux: `~/.local/lib/process-automator`), links `procauto` into `~/.local/bin`, and starts the scheduler (and on macOS the menu bar app) now and at login. `procauto setup` then offers templates (if any), asks which model backend to use, and signs in to what your routines use.

- **Update**: `procauto update` (or Update in the menu)
- **Uninstall**: `procauto uninstall`; `--purge` also removes your config, routines, history, logs and secrets
- **Homebrew** (from a private tap, once you've been invited): `brew install --cask process-automator` on macOS, `brew install process-automator` on Linux, with `HOMEBREW_GITHUB_API_TOKEN` set to a token that can read this repository
- **Ask an AI agent**: tell Claude Code (or another coding agent) to "find the process-automator repo and install it". It follows [`AGENTS.md`](AGENTS.md), asking you for each choice.

On Linux, sign-ins keep their secrets in the Secret Service keyring through `secret-tool` (package `libsecret-tools`); there's no menu bar app (the CLI and scheduler only).

## Dependencies

- **Claude**: either provide an Anthropic API key (`procauto auth login anthropic`), or use the [Claude Code](https://claude.com/claude-code) CLI signed in to a Claude subscription (`procauto settings set llm.backend claude-code`).
- **MCP servers**: whatever your routines use. Add one with `procauto plugins add`, no code needed: see [Adding a plugin](docs/Guides/Adding%20a%20plugin.md). Remote servers need a sign-in (`procauto auth login <name>`); `procauto auth status` shows what's missing.

## Use it from Claude

The whole CLI is also an MCP server, so you can ask Claude to write routines, check them, dry-run them, add plugins, sign in, or tell you why one failed:

```sh
claude mcp add procauto -- procauto mcp serve
```

Every command is a tool. Claude asks you before anything that changes something (real runs, enabling, deleting, settings). Routines it creates start disabled. Sign-ins and keys are entered by you in the browser, never through the chat. What it changed is logged (`procauto paths` shows where).

## Lightweight

Idle, the whole setup uses about 28 MB of memory and effectively no CPU (measured on macOS):

| Process | Memory |
|---|---|
| `procauto daemon` (scheduler, checks every 30s) | ~17 MB |
| automator-ui (menu bar indicator, settings) | ~11 MB; more while a window is open (SwiftUI), mostly freed when it closes |

The model (`claude -p` or API calls) and local MCP server processes start only while a routine runs, then exit.

## Development

```sh
make deps      # dev tools (Go, golangci-lint) via Homebrew
make test      # Go + Swift tests
make lint
make install   # build, sign, install to ~/Applications, (re)start; CLI linked to ~/.local/bin
make help      # everything else
```

Versions come from git tags (`vX.Y.Z`); untagged builds are `X.Y.Z-dev+<sha>` (`0.1.0-dev+<sha>` before the first tag). Cut a release with `make release PART=major|minor|patch` (the first one is `v0.1.0`), then push the tag: the Release workflow (`.github/workflows/release.yml`) builds the universal macOS app (signed with the Developer ID, notarized) and the Linux tarballs (amd64, arm64), publishes them as the tag's GitHub release with `SHA256SUMS`, and updates the Homebrew tap. Running it by hand only builds the artifacts. `make package` and `make package-linux LINUX_ARCH=…` build them locally; `scripts/test-install.sh <artifact>` installs one into a throwaway home, uses it and uninstalls it, touching nothing else.

Build identifiers derive from `BUNDLE_PREFIX` (default `dev`): the bundle ID `dev.procauto`, the login items `dev.procauto.ui` and `dev.procauto.scheduler`, and the Keychain service `dev.procauto`. Set your own reverse-DNS prefix with `make install BUNDLE_PREFIX=com.example`.

### One-time: releases

1. **Developer ID**: in an Apple Developer account, create a *Developer ID Application* certificate and export it with its key as a `.p12`. Put its team ID in `install.sh` (`TEAM_ID=`): installs and updates check releases against it, and the Release workflow refuses to sign without it or with another team.
2. **Notarization**: in App Store Connect › Users and Access › Integrations, create an API key (Developer access) and download its `.p8`.
3. **Repository secrets** (Settings › Secrets and variables › Actions): `APPLE_DEVELOPER_ID_P12_BASE64` (`base64 -i cert.p12`), `APPLE_DEVELOPER_ID_P12_PASSWORD`, `APPLE_API_KEY_P8_BASE64` (`base64 -i AuthKey_….p8`), `APPLE_API_KEY_ID`, `APPLE_API_ISSUER_ID`.
4. **Homebrew tap**: create a private repository named `homebrew-<something>` (e.g. `<owner>/homebrew-tap`), set the variable `HOMEBREW_TAP_REPO` to it and the secret `HOMEBREW_TAP_TOKEN` to a fine-grained token that can write to it. Invite people to both repositories; they install with `brew tap <owner>/<something> git@github.com:<owner>/homebrew-<something>.git`, then `brew install`.

### One-time: signing certificate

Builds are signed with a stable self-signed certificate so macOS keeps permission grants across rebuilds.

1. Keychain Access → Certificate Assistant → Create a Certificate…
2. Name: `Process Automator Dev`, Identity Type: Self-Signed Root, Certificate Type: **Code Signing**
3. Check: `security find-identity -v -p codesigning` lists it

Use a different identity with `make install SIGN_IDENTITY="..."`.
