#!/usr/bin/env bash
# Publishes the release tagged at HEAD from this Mac, as the release workflow
# would: builds every platform's artifact with the repository's own make
# (Linux amd64 and arm64, and the signed macOS app), runs the install test on
# the macOS one, writes SHA256SUMS, pushes the tag and creates its GitHub
# release with the artifacts and the app's install.sh (so a public app installs
# with curl -fsSL https://github.com/<repo>/releases/latest/download/install.sh
# | bash). Nothing is pushed or published unless every step before passed.
# Usage: run from the app's repository (process-automator's, or an app's built
# on it: its Makefile has package and package-linux taking SOURCE_DIR,
# SIGN_IDENTITY, LINUX_ARCH and DIST), at a commit tagged by make release.
#   SIGN_IDENTITY   an identity in your keychains, by name or hash, or
#   SIGN_P12        a .p12 (SIGN_P12_PASSWORD), imported into a throwaway
#                   keychain for the build only
#   INSTALL_SH      the app's installer: the install test uses it and the
#                   release carries it (default: the one next to these
#                   scripts)
#   GH_TOKEN        else the first account gh is signed in to that can push to
#                   the repository
#   DRY_RUN=1       builds and tests everything, then stops before pushing
set -euo pipefail
scripts="$(cd "$(dirname "$0")" && pwd)"
die() { echo "publish: $*" >&2; exit 1; }

tag=$(git describe --tags --exact-match --match 'v[0-9]*.[0-9]*.[0-9]*' HEAD 2>/dev/null) ||
  die "HEAD has no release tag: tag it with make release PART=major|minor|patch first"
[[ -z $(git status --porcelain) ]] || die "the working tree isn't clean"
version=${tag#v}
url=$(git remote get-url origin)
repo=$(sed -E 's#^(https://[^/]+/|[^:]+:)##; s#\.git$##' <<<"$url")
installer=${INSTALL_SH:-$scripts/../install.sh}
[[ -f $installer ]] || die "no installer at $installer"
lower() { tr '[:upper:]' '[:lower:]'; }
[[ $(sed -n 's/^REPO=//p' "$installer" | tr -d '"' | lower) == "$(lower <<<"$repo")" ]] ||
  die "$installer installs another repository's releases (its REPO isn't $repo): set INSTALL_SH to this app's"

# A GitHub token that can push to the repository.
if [[ -z ${GH_TOKEN:-} ]]; then
  for account in $(gh auth status --hostname github.com --json hosts --jq '.hosts["github.com"][].login'); do
    token=$(gh auth token --hostname github.com --user "$account" 2>/dev/null) || continue
    if [[ $(GH_TOKEN=$token gh api "repos/$repo" --jq .permissions.push 2>/dev/null) == true ]]; then
      export GH_TOKEN=$token
      echo "publish: GitHub account $account"
      break
    fi
  done
  [[ -n ${GH_TOKEN:-} ]] || die "no gh account can push to $repo: gh auth login with one that can, or set GH_TOKEN"
fi
! gh release view "$tag" -R "$repo" >/dev/null 2>&1 || die "$repo already has a $tag release"

# The signing identity; a .p12 goes into a throwaway keychain, taken out of
# the search list again however this ends.
identity=${SIGN_IDENTITY:-}
if [[ -n ${SIGN_P12:-} ]]; then
  tmp=$(mktemp -d)
  keychain="$tmp/release.keychain-db" pw=$(uuidgen)
  original=$(security list-keychains -d user | tr -d '"' | xargs)
  # shellcheck disable=SC2086 # the search list is several paths
  trap 'security list-keychains -d user -s $original; security delete-keychain "$keychain" 2>/dev/null; rm -rf "$tmp"' EXIT
  security create-keychain -p "$pw" "$keychain"
  security set-keychain-settings -lut 3600 "$keychain"
  security unlock-keychain -p "$pw" "$keychain"
  security import "$SIGN_P12" -k "$keychain" -P "${SIGN_P12_PASSWORD:-}" -T /usr/bin/codesign >/dev/null
  security set-key-partition-list -S apple-tool:,apple: -s -k "$pw" "$keychain" >/dev/null
  # shellcheck disable=SC2086
  security list-keychains -d user -s "$keychain" $original
  identity=$(security find-identity -p codesigning "$keychain" | sed -n 's/^ *1) \([0-9A-F]\{40\}\) .*/\1/p')
  [[ -n $identity ]] || die "no code-signing identity in $SIGN_P12"
fi
[[ -n $identity ]] || die "set SIGN_IDENTITY (an identity in your keychains) or SIGN_P12 (a .p12 file)"

out="$PWD/build/release/$tag"
rm -rf "$out"
for arch in amd64 arm64; do
  make package-linux LINUX_ARCH="$arch" SOURCE_DIR= DIST="$out"
done
make package SOURCE_DIR= SIGN_IDENTITY="$identity" DIST="$out"

zip=$(find "$out" -name "*-$version-macos.zip")
[[ -n $zip ]] || die "no $version macOS artifact in $out (is the build's version $version?)"
INSTALL_SH=$installer "$scripts/test-install.sh" "$zip"
cp "$installer" "$out/install.sh"
(cd "$out" && shasum -a 256 -- * > SHA256SUMS && cat SHA256SUMS)

if [[ -n ${DRY_RUN:-} ]]; then
  echo "publish: dry run: $tag built and tested in $out; nothing pushed or published"
  exit 0
fi
git push origin "$tag"
gh release create "$tag" "$out"/* -R "$repo" --verify-tag --title "$tag" --generate-notes
echo "publish: $tag released on $repo"
