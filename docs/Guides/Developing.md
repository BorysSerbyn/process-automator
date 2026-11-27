Back to [[Home]]. See also: [[Spec/Requirements/Technical/Distribution|Technical: Distribution]].

# Developing

## Everyday
```sh
make deps      # dev tools (Go, golangci-lint, shellcheck) via Homebrew
make test      # Go + Swift tests
make lint
make ci        # before pushing: tests, lint, Linux/Windows builds, the tray's Linux builds, shellcheck, the Linux package (Docker), the macOS artifact's install test; each step timed (build/timings.tsv)
make install   # build, sign, install to ~/Applications, (re)start; CLI linked to ~/.local/bin
make help      # everything else
```

Development builds are signed with a self-signed certificate so macOS keeps permission grants across rebuilds: create it once as in [[Guides/Getting started#Building it from the checkout|Building it from the checkout]], or use another identity with `make install SIGN_IDENTITY="..."`. Build identifiers derive from `BUNDLE_PREFIX` (default `dev`); set your own reverse-DNS prefix with `make install BUNDLE_PREFIX=com.example`.

## The Linux windows app needs Docker
The Linux package's windows app (`automator-tray/windows`, Fyne) needs cgo against Linux's OpenGL, X11 and Wayland libraries, so `make package-linux` (and so `make ci` and `make publish`) builds it in a Docker container, for amd64 and arm64 alike: any Docker engine that runs Linux containers (OrbStack, Docker Desktop, Colima; on Linux, Docker itself), running. The first build makes the image (`scripts/linux-windows.Dockerfile`) and fills Go's caches (Docker volumes `automator-windows-gomod-go<version>` and `automator-windows-gocache-go<version>`), a few minutes; later ones take seconds, and a binary built before from the same sources is copied from `~/Library/Caches/process-automator-build` without starting Docker (`NO_BUILD_CACHE=1` builds anyway). Without Docker, `make package-linux LINUX_WINDOWS=none` packages without the windows app (the tray then opens every screen in a terminal). The tray and the core stay CGO-free.

Its tests run on the Mac with Fyne's headless test driver (`make test`), and lint builds it there with cgo. To look at its window without a Linux desktop, run a package's `bin/automator-windows setup` under Xvfb in a Linux VM.

## Releases
Versions come from git tags (`vX.Y.Z`); untagged builds are `X.Y.Z-dev+<sha>`. After `make ci`, `make release PART=major|minor|patch` tags HEAD; pushing the tag (`git push origin vX.Y.Z`) runs the Release workflow, which builds every artifact, signs the macOS app with the certificate in the repository's secrets, and publishes them as the tag's GitHub release with `install.sh` and `SHA256SUMS`. From my Mac instead (the fallback), `make publish SIGN_P12=<cert.p12> SIGN_P12_PASSWORD=…` (or `SIGN_IDENTITY=` an identity in my keychains) builds the signed macOS app (Apple silicon) and the Linux tarballs, runs the install test, pushes the tag and publishes them as its GitHub release, with `install.sh` (for the one-line install) and `SHA256SUMS`, through the first `gh` account that can push to the repository. `DRY_RUN=1` stops before pushing. It doesn't update a Homebrew tap. `make package` and `make package-linux LINUX_ARCH=…` build single artifacts; `scripts/test-install.sh <artifact>` installs one into a throwaway home, uses it and uninstalls it.

The GitHub Actions workflows (`.github/workflows/`): CI on every push, Release on a pushed tag, with the secrets below.

### One-time setup
Releases are signed with a self-signed certificate: create one (e.g. `openssl req -x509` with the `codeSigning` extended key usage) and export it with its key as a `.p12`, which `make publish` takes. Keep the `.p12` (in a password manager, with its password): a new certificate makes macOS ask for its grants again after the update, and GitHub never shows a secret back. The release workflow takes it as the repository secrets `SIGNING_CERT_P12_BASE64` (`base64 -i cert.p12`) and `SIGNING_CERT_PASSWORD`, or signs with a Developer ID and notarizes when one is set up (steps 1 to 3, the workflow only).

1. **Developer ID**: in an Apple Developer account, create a *Developer ID Application* certificate and export it with its key as a `.p12`. Put its team ID in `install.sh` (`TEAM_ID=`) and in `cmd/procauto`'s `automator.App` (`TeamID`)
2. **Notarization**: in App Store Connect › Users and Access › Integrations, create an API key (Developer access) and download its `.p8`
3. **Repository secrets** (Settings › Secrets and variables › Actions): `APPLE_DEVELOPER_ID_P12_BASE64` (`base64 -i cert.p12`), `APPLE_DEVELOPER_ID_P12_PASSWORD`, `APPLE_API_KEY_P8_BASE64` (`base64 -i AuthKey_….p8`), `APPLE_API_KEY_ID`, `APPLE_API_ISSUER_ID`
4. **Homebrew tap** (optional): a repository named `homebrew-<something>`; set the variable `HOMEBREW_TAP_REPO` to it and the secret `HOMEBREW_TAP_TOKEN` to a fine-grained token that can write to it

## Building another app on it
Another app (its own name, CLI, plugins and templates) imports `core/` as a Go module: its main package calls `automator.Main` with an `automator.App`, its plugins implement `pluginapi` (and are tested with `pluginapi/plugintest`), and its tests run `Validate` on its `App` and `automator.CheckTemplates` on its templates. It builds with this repository's Makefile and scripts, as [[Spec/Requirements/Technical/Distribution|Technical: Distribution]] describes. Its bug reports go to the repository its `automator.App` names in `BugReports` (none when it's empty: report buttons then say to send the exported logs instead), and anyone who can see that repository sees them. Its releases are private unless its `automator.App` sets `PublicReleases` and its `install.sh` says `PUBLIC=1`: private ones are installed and updated with a GitHub sign-in, public ones with none. Its `install.sh` is this one with its own header (`REPO`, `TEAM_ID`, `NAME`, `COMMAND`, `FOLDER`, `PUBLIC`); publishing it takes `INSTALL_SH` pointing at it.
