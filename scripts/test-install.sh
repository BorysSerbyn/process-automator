#!/usr/bin/env bash
# Installs a release artifact into a throwaway home with install.sh, uses it,
# and uninstalls it with --purge, checking each step. Files only: it never
# starts login items or touches the keychain, so it's safe on a machine with a
# real install (use a BUNDLE_PREFIX of its own when packaging, too).
# Usage: scripts/test-install.sh <artifact>
# INSTALL_SH is the app's installer (default: this repo's install.sh); the
# app's names (NAME, COMMAND, FOLDER) are read from it. When its releases are
# public (PUBLIC=1), it then installs the artifact again as a release served
# by a local stand-in for GitHub's web pages (python3), checking install.sh
# finds and downloads it with curl alone: gh never runs and no token is sent.
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
server=""
trap '[ -z "$server" ] || kill "$server" 2>/dev/null; rm -rf "$sandbox"' EXIT
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
    [ -x "$app/bin/automator-tray" ] || fail "no tray beside the CLI"
    entry="$XDG_CONFIG_HOME/autostart/$command-tray.desktop"
    grep -qx "Exec=\"$app/bin/automator-tray\"" "$entry" || fail "the tray's autostart entry doesn't run the installed tray"
    grep -qx "Name=$name" "$entry" || fail "the tray's autostart entry doesn't name the app"
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
if [ "$(uname -s)" = Linux ]; then
  # The tray turned off at login (its entry hidden, as `login-items disable`
  # does; not run here, as it would turn off the real scheduler) stays off
  # through a reinstall.
  "$cli" login-items status | grep -q "^ui .* starts at login" || fail "login-items doesn't see the tray start at login"
  echo "Hidden=true" >> "$entry"
  "$cli" login-items status | grep -q "^ui .*doesn't start at login" || fail "login-items doesn't see the tray turned off"
  "$installer" --from "$sandbox/$(basename "$artifact")" --yes --files-only >/dev/null
  grep -qx "Hidden=true" "$entry" || fail "reinstalling turned the tray back on at login"
fi
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

if [ "$(install_var PUBLIC)" = 1 ]; then
  if ! command -v python3 >/dev/null; then
    echo "--- public releases: skipped (no python3)"
  else
    echo "--- install from public releases"
    # A release as GitHub's web pages serve it: /<repo>/releases/latest
    # redirects to the latest tag's page, /<repo>/releases/download/<tag>/<file>
    # to where the file is stored. Requests carrying a token are refused and
    # logged.
    base=$(basename "$artifact") v=${base#"$folder"-}
    v=${v%-macos.zip} v=${v%-linux-*.tar.gz}
    mkdir -p "$sandbox/releases/v$v" "$sandbox/fakebin"
    cp "$sandbox/$base" "$sandbox/SHA256SUMS" "$sandbox/releases/v$v/"
    echo "v$v" > "$sandbox/releases/latest"
    cat > "$sandbox/server.py" <<'EOF'
import http.server, os, re, sys, urllib.parse
root, portfile, log = sys.argv[1:4]
class Pages(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        path = urllib.parse.unquote(urllib.parse.urlsplit(self.path).path)
        with open(log, "a") as f:
            f.write(("AUTH " if self.headers.get("Authorization") else "") + path + "\n")
        if self.headers.get("Authorization"):
            return self.send_error(403)
        m = re.fullmatch(r"/(.+)/releases/latest", path)
        if m:
            latest = os.path.join(root, "latest")
            tag = open(latest).read().strip() if os.path.exists(latest) else ""
            return self.redirect(f"/{m[1]}/releases" + (f"/tag/{tag}" if tag else ""))
        m = re.fullmatch(r"/.+/releases/download/([^/]+)/([^/]+)", path)
        if m and os.path.isfile(os.path.join(root, m[1], m[2])):
            return self.redirect(f"/storage/{m[1]}/{m[2]}")
        m = re.fullmatch(r"/storage/([^/]+)/([^/]+)", path)
        if m and os.path.isfile(os.path.join(root, m[1], m[2])):
            body = open(os.path.join(root, m[1], m[2]), "rb").read()
            self.send_response(200)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            return self.wfile.write(body)
        self.send_error(404)
    def redirect(self, to):
        self.send_response(302)
        self.send_header("Location", f"http://127.0.0.1:{self.server.server_port}{to}")
        self.end_headers()
    def log_message(self, *args):
        pass
srv = http.server.HTTPServer(("127.0.0.1", 0), Pages)
with open(portfile + ".tmp", "w") as f:
    f.write(str(srv.server_port))
os.rename(portfile + ".tmp", portfile)
srv.serve_forever()
EOF
    python3 -I "$sandbox/server.py" "$sandbox/releases" "$sandbox/port" "$sandbox/requests.log" &
    server=$!
    for _ in $(seq 50); do [ -s "$sandbox/port" ] && break; sleep 0.1; done
    [ -s "$sandbox/port" ] || fail "the stand-in for GitHub didn't start"
    sed "s|^WEB=.*|WEB=http://127.0.0.1:$(cat "$sandbox/port")|" "$installer" > "$sandbox/install-public.sh"
    # No gh (one first on PATH records being run): with no token set, a private
    # installer would ask it for one; with one set, the token must not be sent.
    printf '#!/bin/sh\necho "$@" >> "%s"\nexit 1\n' "$sandbox/gh-ran" > "$sandbox/fakebin/gh"
    chmod +x "$sandbox/fakebin/gh"
    public() { PATH="$sandbox/fakebin:$PATH" bash "$sandbox/install-public.sh" "$@"; }
    with_token() { GH_TOKEN=test-install-token GITHUB_TOKEN=test-install-token "$@"; }
    unset GH_TOKEN GITHUB_TOKEN
    # refused <what> <text> <install.sh args>...: install.sh fails, saying text and nothing about signing in.
    refused() {
      local what=$1 text=$2 out
      shift 2
      if out=$(public "$@" --yes --files-only 2>&1 </dev/null); then fail "$what: installed"; fi
      case $out in *"$text"*) ;; *) fail "$what: $out" ;; esac
      ! grep -Eiq '(^|[^a-z])gh([^a-z]|$)|token|sign[- ]?in|signed in' <<<"$out" || fail "$what mentions signing in: $out"
    }

    with_token public --yes --files-only </dev/null
    [ -L "$cli" ] || fail "no CLI link at $cli after installing the latest public release"
    "$cli" version
    public --version "v$v" --yes --files-only </dev/null
    "$cli" uninstall --purge --yes
    grep -qx "/[^ ]*/releases/latest" "$sandbox/requests.log" || fail "install.sh didn't ask for the latest release"
    grep -q "/releases/download/v$v/$base$" "$sandbox/requests.log" || fail "install.sh didn't download $base"

    refused "a release without this machine's download" "release v0.0.1 has no" --version v0.0.1
    echo "0000000000000000000000000000000000000000000000000000000000000000  $base" > "$sandbox/releases/v$v/SHA256SUMS"
    refused "a tampered download" "checksum mismatch"
    rm "$sandbox/releases/latest"
    refused "no releases" "has no releases yet"

    [ ! -e "$sandbox/gh-ran" ] || fail "gh was run: $(cat "$sandbox/gh-ran")"
    ! grep -q '^AUTH ' "$sandbox/requests.log" || fail "a token was sent: $(grep '^AUTH ' "$sandbox/requests.log")"
  fi
fi
echo "--- ok"
