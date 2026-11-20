#!/usr/bin/env bash
# Renders the Homebrew cask (macOS) and formula (Linux) for a published release
# into a checkout of the tap: Casks/process-automator.rb, Formula/process-automator.rb.
# Usage: homebrew.sh <tag> <dist-dir> <tap-dir>
#   <dist-dir> holds the release's artifacts and SHA256SUMS.
# The files' API URLs come from the published release (gh, signed in); set
# RELEASE_ASSETS to a file of "<name> <url>" lines instead to render offline.
set -euo pipefail
tag=${1:?usage: homebrew.sh <tag> <dist-dir> <tap-dir>} dist=$2 tap=$3
here="$(cd "$(dirname "$0")" && pwd)"
v=${tag#v}
repo=$(sed -n 's/^REPO=//p' "$here/../install.sh")

if [ -n "${RELEASE_ASSETS:-}" ]; then
  assets=$(cat "$RELEASE_ASSETS")
else
  assets=$(gh api "repos/$repo/releases/tags/$tag" --jq '.assets[] | "\(.name) \(.url)"')
fi
url_of() {
  local u
  u=$(awk -v n="$1" '$1 == n { print $2 }' <<<"$assets")
  [ -n "$u" ] || { echo "homebrew.sh: release $tag has no $1" >&2; exit 1; }
  echo "$u"
}
sha_of() {
  local s
  s=$(awk -v n="$1" '{ x = $2; sub(/^\*/, "", x) } x == n { print $1 }' "$dist/SHA256SUMS")
  [ -n "$s" ] || { echo "homebrew.sh: SHA256SUMS doesn't list $1" >&2; exit 1; }
  echo "$s"
}
# The labels the build gave the login items, from the artifacts' install.env.
macos_zip="process-automator-$v-macos.zip"
macos_env=$(unzip -p "$dist/$macos_zip" "Process Automator.app/Contents/Resources/install.env")
amd64_tar="process-automator-$v-linux-amd64.tar.gz" arm64_tar="process-automator-$v-linux-arm64.tar.gz"
linux_env=$(tar -xzOf "$dist/$amd64_tar" "${amd64_tar%.tar.gz}/share/process-automator/install.env")
env_val() { sed -n "s/^$2=//p" <<<"$1"; }

mkdir -p "$tap/Casks" "$tap/Formula"
sed -e "s|__TAG__|$tag|g" -e "s|__VERSION__|$v|g" -e "s|__REPO__|$repo|g" \
  -e "s|__MACOS_URL__|$(url_of "$macos_zip")|g" -e "s|__MACOS_SHA256__|$(sha_of "$macos_zip")|g" \
  -e "s|__UI_LABEL__|$(env_val "$macos_env" UI_LABEL)|g" -e "s|__SCHED_LABEL__|$(env_val "$macos_env" SCHED_LABEL)|g" \
  -e "s|__KEYCHAIN_SERVICE__|$(env_val "$macos_env" KEYCHAIN_SERVICE)|g" \
  "$here/../packaging/homebrew/process-automator.cask.rb.in" > "$tap/Casks/process-automator.rb"
sed -e "s|__TAG__|$tag|g" -e "s|__VERSION__|$v|g" -e "s|__REPO__|$repo|g" \
  -e "s|__AMD64_URL__|$(url_of "$amd64_tar")|g" -e "s|__AMD64_SHA256__|$(sha_of "$amd64_tar")|g" \
  -e "s|__ARM64_URL__|$(url_of "$arm64_tar")|g" -e "s|__ARM64_SHA256__|$(sha_of "$arm64_tar")|g" \
  -e "s|__SCHED_LABEL__|$(env_val "$linux_env" SCHED_LABEL)|g" \
  "$here/../packaging/homebrew/process-automator.formula.rb.in" > "$tap/Formula/process-automator.rb"
echo "$tap/Casks/process-automator.rb"
echo "$tap/Formula/process-automator.rb"
