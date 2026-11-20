#!/usr/bin/env bash
# Installs a release artifact into a throwaway home with install.sh, uses it,
# and uninstalls it with --purge, checking each step. Files only: it never
# starts login items or touches the keychain, so it's safe on a machine with a
# real install (use a BUNDLE_PREFIX of its own when packaging, too).
# Usage: scripts/test-install.sh <artifact>
# INSTALL_SH is the app's installer (default: this repo's install.sh); the
# app's names (NAME, COMMAND, FOLDER) are read from it.
set -euo pipefail
artifact="$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"
repo="$(cd "$(dirname "$0")/.." && pwd)"
installer="$(cd "$(dirname "${INSTALL_SH:-$repo/install.sh}")" && pwd)/$(basename "${INSTALL_SH:-install.sh}")"
install_var() { sed -n "s/^$1=//p" "$installer" | tr -d '"'; }
name=$(install_var NAME) command=$(install_var COMMAND) folder=$(install_var FOLDER)
[ -n "$name" ] && [ -n "$command" ] && [ -n "$folder" ] || { echo "test-install: $installer doesn't name the app" >&2; exit 1; }
# Resolved (pwd -P), so it matches the paths the app reports: TMPDIR may end
# in a slash, and macOS's /var is /private/var.
sandbox=$(cd "$(mktemp -d "${TMPDIR:-/tmp}/$command-install-test.XXXXXX")" && pwd -P)
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
"$installer" --from "$sandbox/$(basename "$artifact")" --yes --files-only

cli="$HOME/.local/bin/$command"
[ -L "$cli" ] || fail "no CLI link at $cli"
case $(uname -s) in
  Darwin)
    app="$HOME/Applications/$name.app"
    [ -d "$app" ] || fail "no app at $app"
    codesign --verify --deep --strict "$app" || fail "the installed app's signature doesn't verify"
    ls "$HOME"/Library/LaunchAgents/*."$command".ui.plist "$HOME"/Library/LaunchAgents/*."$command".scheduler.plist >/dev/null ||
      fail "no LaunchAgents"
    grep -q "$app/Contents/MacOS/$command" "$HOME"/Library/LaunchAgents/*."$command".scheduler.plist ||
      fail "the scheduler's LaunchAgent doesn't run the installed app"
    ;;
  Linux)
    app="$HOME/.local/lib/$folder"
    [ -x "$app/bin/$command" ] || fail "no app at $app"
    unit="$XDG_CONFIG_HOME/systemd/user/$command-scheduler.service"
    grep -q "^ExecStart=$app/bin/$command daemon$" "$unit" || fail "the scheduler unit doesn't run the installed app"
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
# The templates the artifact ships list (or, when it ships none, it says so).
tmpl=$("$cli" paths --json | sed -n 's/^  "templates": "\(.*\)",*$/\1/p')
[ -d "$tmpl" ] || fail "no templates folder at $tmpl"
shipped=$(find "$tmpl" -name '*.prompt' ! -name '_*' | wc -l | tr -d ' ')
if [ "$shipped" -eq 0 ]; then
  expect "templates list doesn't say none ship" "No templates" "$cli" templates list --all
else
  [ "$("$cli" templates list --all --json | grep -c '"id":')" -eq "$shipped" ] || fail "templates list doesn't list the $shipped templates shipped"
fi
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
leftover=$(find "$HOME" \( -name "*$command*" -o -name "$folder" \) -print)
[ -z "$leftover" ] || fail "left behind: $leftover"
echo "--- ok"
