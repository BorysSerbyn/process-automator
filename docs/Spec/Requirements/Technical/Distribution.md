Back to [[Home]]. See also [[Spec/Requirements/Functional/Distribution|Functional: Distribution]], [[Spec/Requirements/Technical/Technical Requirements|Technical Requirements]]. Why it's built this way: [[Spec/Technical Decisions|Technical Decisions]].

## Repository
- **The `process-automator` repository** is private (its GitHub path is `REPO` in `install.sh`). `make ci` runs the checks on my Mac before a push: Go tests with `-race`, the Swift tests, lint, Linux and Windows cross-compiles of the core, shellcheck, and an install test of the macOS artifact. The GitHub Actions workflows that ran them (and the Linux install test) on every push are kept, disabled

## Versioning
- **One version source: git tags** `vX.Y.Z`, embedded at build time into the Go binary (linker flags) and the Swift app (`CFBundleShortVersionString`), so both report the same version
- **Untagged builds** get a dev version: `<next patch>-dev+<sha>` (`.dirty` with uncommitted changes), and `0.1.0-dev+<sha>` before the first tag; the first release is `v0.1.0`
- `make release PART=major|minor|patch` bumps and tags

## Build identifiers
| What | Value |
|---|---|
| CLI | `procauto` (`Command`) |
| Bundle ID | `<BUNDLE_PREFIX>.procauto` (`BUNDLE_PREFIX` defaults to `dev`) |
| The core's signing identifier | The bundle ID, so the notify plugin's notifications are the app's |
| macOS login items | `<prefix>.procauto.ui`, `<prefix>.procauto.scheduler` |
| Linux login item | `procauto-scheduler.service` (systemd user unit) |
| Secrets service | `<prefix>.procauto`, the bundle ID |
| Development signing identity | `Process Automator Dev` (`SIGN_IDENTITY`, `<Name> Dev`) |

- **Said three times, kept in step**: the binary's `automator.App`, `install.sh` (`NAME`, `COMMAND`, `FOLDER`, `REPO`, `TEAM_ID`) and the Makefile (`BUNDLE_PREFIX`); `cmd/procauto`'s test fails when they differ. The Makefile reads the names from `install.sh`, and the build writes them into `install.env` (for the installer) and the app's `Info.plist` (`CFBundleName`, `ProcAutoCommand`), which automator-ui reads its name and its CLI from

## Building another app
- **The build is parameterised**: another app runs this repository's Makefile targets (`build`, `bundle`, `package`, `package-linux`, `install`, `uninstall`) from a checkout at the version its `go.mod` pins, passing its own `INSTALL_SH` (its names), `GO_DIR` and `MAIN` (its module and main package), `TEMPLATES`, `LOGO` (a folder with `AppIcon.icns`, `glyph.svg`, `dot.svg`), `BUNDLE_PREFIX`, `SOURCE_DIR`, `BUILD` and `VERSION`
- **One automator-ui** serves every app: it has no names of its own, and the bundle gives it the app's
- **Its scripts are this repository's**: `scripts/bundle.sh`, `package.sh`, `install-app.sh`, `test-install.sh` (`INSTALL_SH` for the app's installer) and `version.sh` take the app's names from `install.env` or its `install.sh`; its `install.sh` is this one with its own names in the header

## Releases
- **Built from git tags on my Mac**: `make publish` (`scripts/publish.sh`), at a commit tagged `vX.Y.Z`, builds every artifact with the repository's `make package` and `make package-linux`, signs the macOS app with the given identity or `.p12` (a throwaway keychain), runs the install test, then pushes the tag and publishes them as its GitHub release with a `SHA256SUMS` file; nothing is pushed unless every step passed. An app built on the engine runs the same script from its repository
- **macOS**: `<Folder>-<version>-macos.zip` (`process-automator-…`), the `.app` with the Go core inside, for Apple silicon (there's no Intel build), zipped with `ditto`. Signed with a Developer ID (hardened runtime) and notarized with the ticket stapled when the release workflow has one (`APPLE_DEVELOPER_ID_P12_*`, `APPLE_API_KEY_*`); otherwise with a self-signed certificate (`SIGNING_CERT_P12_BASE64`, `SIGNING_CERT_PASSWORD`), the same for every release
- **Linux**: `<Folder>-<version>-linux-<amd64|arm64>.tar.gz`: `bin/<Command>` and `share/<Folder>/` (templates, the installer, the systemd unit template)
- **Every artifact carries its installer** (`install-app.sh`) and what the build named things (`install.env`: the app's name, command and folder, version, bundle ID, login item labels, secrets service), next to its templates: `Contents/Resources/` in the `.app`, `share/<Folder>/` in the Linux tree
- **Pinned once**: the repository releases come from (`REPO`) and the Apple team that signs them (`TEAM_ID`) are set in `install.sh`, as in the binary's `automator.App`; the release workflow refuses to sign with another team, or self-signed while `TEAM_ID` is set. While it's empty, installs and updates accept any valid signature
- **Locally**: `make package` and `make package-linux LINUX_ARCH=amd64|arm64` build the artifacts; `scripts/test-install.sh <artifact>` installs one into a throwaway home with `install.sh`, uses it and uninstalls it, files only
- **Homebrew tap** in a private repository, shared with invited people; the release workflow renders the cask and formula (`scripts/homebrew.sh`, from `packaging/homebrew/`) and pushes them to the tap (`HOMEBREW_TAP_REPO`, `HOMEBREW_TAP_TOKEN`). They download through the GitHub API with `HOMEBREW_GITHUB_API_TOKEN`
  - **Cask** (`process-automator`): installs the app and links `procauto`; its postflight sets up the login items (`install-app.sh login-items`); `auto_updates`, since the app updates itself; uninstalling removes the login items, zap the data and logs
  - **Formula** (`process-automator`): installs the Linux tree and links `procauto`; its caveats give the command that sets up the scheduler's unit; updated with `brew upgrade`

## Installing
- **One installer**: `install-app.sh` inside the artifact installs the app it's part of. `make install`, `install.sh`, `update`, `uninstall` and the Homebrew cask all run it
  - `install [--to <path>]`: copies the app into place (beside the old one, then swapped), links `procauto` into `BIN_DIR` (`~/.local/bin`), sets up and (re)starts the login items. Defaults (`INSTALL_DIR`): macOS `~/Applications/Process Automator.app`, Linux `~/.local/lib/process-automator`. `--to` replaces the app at that path and leaves the link alone
  - `login-items`: sets up and (re)starts the login items for the app where it is
  - `uninstall [--purge]`: stops and removes the login items, the `procauto` link (only if it points into this app) and the app; `--purge` also the data folder (config, routines, history), logs and the secrets under the build's service
  - **Login items**: macOS LaunchAgents (`<prefix>.procauto.ui`, `<prefix>.procauto.scheduler`, logging to `~/Library/Logs/process-automator/`); Linux the systemd user unit `procauto-scheduler.service` (logging to the journal), enabled on first install. Reinstalling keeps whether each starts at login (a LaunchAgent turned off is loaded and turned off again). The scheduler's PATH adds `~/.local/bin` and Homebrew's to the system default. Without a systemd user session it says to start `procauto daemon` another way
  - `--files-only` (`PROCAUTO_INSTALL_FILES_ONLY=1`) writes the files but never touches launchd, systemd or the secrets store
- **`install.sh`** at the repository root, run from a checkout or piped (`gh api repos/<owner>/process-automator/contents/install.sh -H 'Accept: application/vnd.github.raw' | bash`): checks the prerequisites, finds the latest release (or `--version`), downloads this platform's artifact and `SHA256SUMS` (through `gh` when it's signed in, else the GitHub API with `GH_TOKEN`/`GITHUB_TOKEN`; without those, it uses the first account `gh` is signed in to that can read the repository, the active one first, and stops naming the accounts when none can), verifies them like `update`, unpacks the artifact and runs its installer. `--from <archive>` installs a local artifact (checked against a `SHA256SUMS` beside it when there is one); `PROCAUTO_INSTALL_UNSIGNED=1` accepts any valid signature (testing local builds). In a terminal it then offers `procauto setup`; with `--yes` it asks nothing and says to run it
- **`procauto uninstall [--purge] [--yes]`** runs the installed app's own installer; it asks first, and without a terminal it needs `--yes`. Not an MCP tool
- **Homebrew formula installs** are updated and removed with brew: `update` and `uninstall` say so instead

## Building from a checkout
- **macOS**: `make deps` (Go and the build tools, from the `Brewfile`), then `make install`: builds the core and automator-ui, assembles the `.app` with the core inside, signs it with `SIGN_IDENTITY`, and runs the installer, which stops the running core and UI, swaps the bundle and restarts them. `make uninstall` (`PURGE=1` to purge) runs the installer's uninstall
- **Stable signing identity**: development builds are signed with the same self-signed certificate (`Process Automator Dev`) on every build, with a stable bundle ID and install path, so permission grants and Keychain access survive rebuilds
- **Linux**: `make package-linux LINUX_ARCH=…`, then `install.sh --from <the tarball>`
- **The checkout is remembered** (`SOURCE_DIR`, overridable by `update.source-dir`) for updates while no published release has a download for this machine

## Update command
- **From releases**: `update` reads releases through the GitHub API, signed in with `GH_TOKEN`/`GITHUB_TOKEN` when set, else with an account the GitHub CLI is signed in to for github.com that can read the release repository: the active one if it can, else the first of the others that can (`gh auth status --json hosts`, `gh auth token --user`; each tried on the repository). When none can, it uses the active account and its error names the accounts tried; with no sign-in at all it reads unauthenticated, which a private repository refuses (the error says to sign in). Tokens are never printed. It takes the highest `vX.Y.Z` release that's neither a draft nor a pre-release and is newer than the running version
- **Verifies before running anything**: the artifact's SHA256 against the release's `SHA256SUMS`; on macOS its code signature (`codesign --verify --deep --strict`) and, given the build's `TEAM_ID`, that team's Developer ID and Apple's notarization (`spctl`). Any mismatch installs nothing
- **Replaces the app where it is**: runs the new artifact's installer with `install --to <the installed app>`, restarting the login items
- **Releases it can't read stop it**: when the releases can't be read (no access, no network), `update` says why and installs nothing
- **From a checkout**: when the releases were read and none has a download for this machine (or there are none), `update` builds the checkout's newest release tag in a temporary git worktree (the checkout itself is never touched) and installs it as above; `install.sh` says to build instead
- `--check` only reports (with a `note` on what installing involves: a download, or a build from the checkout taking minutes); `--detach` installs in the background (for frontends), logging to `update.log` and reporting the result as a notification; `locks/update.lock` keeps two installs from running at once
- **Progress**: the process installing records its progress in `update-progress.json` as it goes: the release, the step in words (checking, downloading, checking it's genuine, installing, or building from the checkout), the latest line the step printed, and how it ended. `update progress [--json]` reports it: `state` `installing` (the lock is held, or an update started in the last 30 seconds), `done`, `failed` (with the error; one that stopped without finishing is failed too) or `none`

## First-run setup
- **`setup`** in a terminal walks through the steps in order, asking before each (except `auto` ones) and running the same commands as below; done steps are skipped, and it can run again any time
- **`setup --json`** changes nothing: it lists the steps by kind, each with `done`, `optional`, `auto`, `status`, `ask` and the `commands` that do it (placeholders for my answers), for an LLM or automator-ui guiding me. An `auto` step is done without asking: whoever guides me runs its commands as soon as it comes to it

| Kind | Step |
|---|---|
| `templates` | Optional: which projects I work on (when templates are made for any), then which templates to install, with their parameters |
| `backend` | `llm.backend`, and its key or token |
| `usage` | While Claude Code's `/usage` can't be read (it answered without a usage report, or 3 tries in a row failed): the status line fallback |
| `mcp` | On `claude-code`: Claude Code has the app's MCP server at user scope (`mcp connect`); `auto` |
| `routine` | A routine that can't run, and why |
| `sign-in` | Each server the routines use that isn't signed in, with its `prepare` steps first |
| `turn-on` | Each routine, done once it's trusted and not paused: `routines trust <name> --yes --resume` trusts it and resumes it in one step. Untrusted, it's required; trusted but paused, it's optional (pausing a routine never makes setup unfinished) |

- **Setup is done** when every step that isn't optional is
- **Setup left shows in run status**: the scheduler checks setup on every pass and records the steps still to do (`setupLeft`), and so does every command that succeeds except the read-only ones, plus `usage` and `setup --json`, so the menu and the setup window always agree; it's written only when what's left changed; unfinished setup makes health `error`, with a reason saying what's left

## Installing through an LLM
- **`AGENTS.md`** at the repository root (where coding agents look) holds the install instructions for LLMs: check prerequisites, install (`./install.sh --yes`, or build from the checkout), then follow `setup --json` step by step, asking me each step's question (and doing `auto` steps without asking)
- **Every step runs without a terminal**: CLI commands with `--json`, and `--yes` once I've answered, the same ones the installer and `setup` use; sign-ins open my browser as they do from the CLI
