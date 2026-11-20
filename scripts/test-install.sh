#!/usr/bin/env bash
# Installs a release artifact into a throwaway home with install.sh, uses it,
# and uninstalls it with --purge, checking each step. Files only: it never
# starts login items or touches the keychain, so it's safe on a machine with a
# real install (use a BUNDLE_PREFIX of its own when packaging, too).
# Usage: scripts/test-install.sh <artifact>
set -euo pipefail
artifact="$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"
repo="$(cd "$(dirname "$0")/.." && pwd)"
# Resolved (pwd -P), so it matches the paths the app reports: TMPDIR may end
# in a slash, and macOS's /var is /private/var.
sandbox=$(cd "$(mktemp -d "${TMPDIR:-/tmp}/procauto-install-test.XXXXXX")" && pwd -P)
trap 'rm -rf "$sandbox"' EXIT
export HOME="$sandbox/home" XDG_CONFIG_HOME="$sandbox/home/.config" PROCAUTO_INSTALL_FILES_ONLY=1
# Local builds carry an ad-hoc signature, not the release's Developer ID.
export PROCAUTO_INSTALL_UNSIGNED=${PROCAUTO_INSTALL_UNSIGNED-1}
unset INSTALL_DIR BIN_DIR
mkdir -p "$HOME"
fail() { echo "test-install: $*" >&2; exit 1; }
sum() { if command -v sha256sum >/dev/null; then sha256sum "$@"; else shasum -a 256 "$@"; fi; }

# A SHA256SUMS beside the artifact, as a release has.
cp "$artifact" "$sandbox/"
(cd "$sandbox" && sum "$(basename "$artifact")" > SHA256SUMS)
echo "--- install"
"$repo/install.sh" --from "$sandbox/$(basename "$artifact")" --yes --files-only

cli="$HOME/.local/bin/procauto"
[ -L "$cli" ] || fail "no CLI link at $cli"
case $(uname -s) in
  Darwin)
    app="$HOME/Applications/Process Automator.app"
    [ -d "$app" ] || fail "no app at $app"
    codesign --verify --deep --strict "$app" || fail "the installed app's signature doesn't verify"
    ls "$HOME"/Library/LaunchAgents/*.procauto.ui.plist "$HOME"/Library/LaunchAgents/*.procauto.scheduler.plist >/dev/null ||
      fail "no LaunchAgents"
    grep -q "$app/Contents/MacOS/procauto" "$HOME"/Library/LaunchAgents/*.procauto.scheduler.plist ||
      fail "the scheduler's LaunchAgent doesn't run the installed app"
    ;;
  Linux)
    app="$HOME/.local/lib/process-automator"
    [ -x "$app/bin/procauto" ] || fail "no app at $app"
    unit="$XDG_CONFIG_HOME/systemd/user/procauto-scheduler.service"
    grep -q "^ExecStart=$app/bin/procauto daemon$" "$unit" || fail "the scheduler unit doesn't run the installed app"
    ;;
esac

echo "--- use"
"$cli" version
"$cli" paths
case $("$cli" paths --json) in *"$HOME"*) ;; *) fail "paths outside the sandbox home" ;; esac
expect() { # expect <what> <text> <command>...: the command's output contains text
  local what=$1 text=$2 out
  shift 2
  out=$("$@" 2>&1 </dev/null || true)
  case $out in *"$text"*) ;; *) fail "$what: $out" ;; esac
}
expect "templates list doesn't say none ship" "No templates" "$cli" templates list --all
expect "setup --json lists no backend step" '"step": "backend"' "$cli" setup --json
expect "login-items doesn't see the installed login items" '"installed": true' "$cli" login-items status --json
expect "uninstall without a terminal didn't ask for --yes" "--yes" "$cli" uninstall
"$cli" settings set history.retention-days 30 >/dev/null
data=$("$cli" paths --json | sed -n 's/^  "data": "\(.*\)",*$/\1/p')
[ -d "$data" ] || fail "no data folder at $data"

echo "--- uninstall"
"$cli" uninstall --purge --yes
[ ! -e "$app" ] || fail "the app is still there"
[ ! -L "$cli" ] || fail "the CLI link is still there"
[ ! -e "$data" ] || fail "--purge left $data"
leftover=$(find "$HOME" \( -name '*procauto*' -o -name 'process-automator' \) -print)
[ -z "$leftover" ] || fail "left behind: $leftover"
echo "--- ok"
