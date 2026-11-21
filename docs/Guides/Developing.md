Back to [[Home]]. See also: [[Spec/Requirements/Technical/Distribution|Technical: Distribution]].

# Developing

## Everyday
```sh
make deps      # dev tools (Go, golangci-lint) via Homebrew
make test      # Go + Swift tests
make lint
make install   # build, sign, install to ~/Applications, (re)start; CLI linked to ~/.local/bin
make help      # everything else
```

Development builds are signed with a self-signed certificate so macOS keeps permission grants across rebuilds: create it once as in [[Guides/Getting started#Building it from the checkout|Building it from the checkout]], or use another identity with `make install SIGN_IDENTITY="..."`. Build identifiers derive from `BUNDLE_PREFIX` (default `dev`); set your own reverse-DNS prefix with `make install BUNDLE_PREFIX=com.example`.

## Releases
Versions come from git tags (`vX.Y.Z`); untagged builds are `X.Y.Z-dev+<sha>`. `make release PART=major|minor|patch` tags a release; pushing the tag runs the Release workflow, which builds the signed macOS app (Apple silicon) and the Linux tarballs, publishes them with `SHA256SUMS`, and updates the Homebrew tap. `make package` and `make package-linux LINUX_ARCH=…` build them locally; `scripts/test-install.sh <artifact>` installs one into a throwaway home, uses it and uninstalls it.

### One-time setup
Releases are signed with a Developer ID when one is set up (steps 1 to 3), otherwise with a self-signed certificate: create one (e.g. `openssl req -x509` with the `codeSigning` extended key usage), export it with its key as a `.p12`, and set the repository secrets `SIGNING_CERT_P12_BASE64` (`base64 -i cert.p12`) and `SIGNING_CERT_PASSWORD`. Keep the `.p12`: a new certificate makes macOS ask for its grants again after the update.

1. **Developer ID**: in an Apple Developer account, create a *Developer ID Application* certificate and export it with its key as a `.p12`. Put its team ID in `install.sh` (`TEAM_ID=`) and in `cmd/procauto`'s `automator.App` (`TeamID`)
2. **Notarization**: in App Store Connect › Users and Access › Integrations, create an API key (Developer access) and download its `.p8`
3. **Repository secrets** (Settings › Secrets and variables › Actions): `APPLE_DEVELOPER_ID_P12_BASE64` (`base64 -i cert.p12`), `APPLE_DEVELOPER_ID_P12_PASSWORD`, `APPLE_API_KEY_P8_BASE64` (`base64 -i AuthKey_….p8`), `APPLE_API_KEY_ID`, `APPLE_API_ISSUER_ID`
4. **Homebrew tap** (optional): a repository named `homebrew-<something>`; set the variable `HOMEBREW_TAP_REPO` to it and the secret `HOMEBREW_TAP_TOKEN` to a fine-grained token that can write to it

## Building another app on it
Another app (its own name, CLI, plugins and templates) imports `core/` as a Go module: its main package calls `automator.Main` with an `automator.App`, its plugins implement `pluginapi`, and its tests run `automator.CheckTemplates` on its templates. It builds with this repository's Makefile and scripts, as [[Spec/Requirements/Technical/Distribution|Technical: Distribution]] describes.
