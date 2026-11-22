#!/usr/bin/env bash
# Tests install-app.sh's functions under its own `set -euo pipefail`, against
# fake OS tools that behave like the real ones, so it runs anywhere and never
# touches launchd, the Keychain or a keyring:
#   - LaunchAgents (stop_agent, load_agent, disabled): `launchctl print` of an
#     agent that isn't loaded exits 113. An update stops the agents, then
#     (re)loads each one: stopping one that isn't loaded mustn't end the script.
#   - purge_secrets on both OSes: a locked store is reported, not "removed".
#   - The Linux app menu entry: written with the tray's path quoted (a home
#     with a space, & and $), and removed.
set -euo pipefail
repo="$(cd "$(dirname "$0")/.." && pwd)"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fail() { echo "test-launch-agents: $*" >&2; exit 1; }

mkdir -p "$tmp/bin" "$tmp/state"
# The fake keeps one file per loaded agent; bootstrap takes the label from the
# plist's file name, as ours are named <label>.plist.
cat > "$tmp/bin/launchctl" <<'EOF'
#!/usr/bin/env bash
state=$FAKE_LAUNCHD
echo "$*" >> "$state/calls"
case $1 in
  print)
    label=${2##*/}
    [ -f "$state/$label" ] || { echo "Could not find service \"$label\"" >&2; exit 113; }
    printf '%s = {\n\tstate = running\n\tpid = 99999999\n}\n' "$2" ;;
  print-disabled) printf 'disabled services = {\n\t"off.agent" => disabled\n}\n' ;;
  bootout)
    label=${2##*/}
    [ -f "$state/$label" ] || { echo "Boot-out failed: 3: No such process" >&2; exit 3; }
    rm -f "$state/$label" ;;
  bootstrap)
    label=$(basename "$3" .plist)
    [ ! -f "$state/$label" ] || { echo "Bootstrap failed: 5: Input/output error" >&2; exit 5; }
    : > "$state/$label" ;;
  enable|disable) ;;
  *) echo "fake launchctl: unexpected $*" >&2; exit 64 ;;
esac
EOF
chmod +x "$tmp/bin/launchctl"
export PATH="$tmp/bin:$PATH" FAKE_LAUNCHD="$tmp/state"

# Each case in its own bash, with install-app.sh's functions and shell options.
run() {
  bash -c '. "$1"; shift; "$@"' _ "$repo/scripts/install-app.sh" "$@"
}
loaded() { [ -f "$tmp/state/$1" ]; }

# Stopping an agent that isn't loaded: fine, twice in a row.
run stop_agent dev.test.ui || fail "stop_agent of an agent that isn't loaded failed"
run stop_agent dev.test.ui || fail "stop_agent failed the second time"

# An update: the agents are stopped, then loaded (each stopped again first).
: > "$tmp/state/dev.test.scheduler"
run stop_agent dev.test.scheduler || fail "stop_agent of a loaded agent failed"
loaded dev.test.scheduler && fail "stop_agent left the agent loaded"
run load_agent "$tmp/dev.test.scheduler.plist" dev.test.scheduler || fail "load_agent after a stop failed"
loaded dev.test.scheduler || fail "load_agent didn't load the agent"

# Reloading an agent that's loaded: stopped, then loaded again.
run load_agent "$tmp/dev.test.scheduler.plist" dev.test.scheduler || fail "load_agent of a loaded agent failed"
loaded dev.test.scheduler || fail "the reloaded agent isn't loaded"

# disabled reads the list whatever grep does.
run disabled off.agent || fail "disabled missed a disabled agent"
if run disabled dev.test.scheduler; then fail "disabled reported an enabled agent"; fi

# purge_secrets: fake security and secret-tool keep a count of the service's
# items, and refuse to change anything while "locked".
cat > "$tmp/bin/security" <<'EOF2'
#!/usr/bin/env bash
n=$(cat "$FAKE_STORE/items")
case $1 in
  delete-generic-password)
    [ ! -f "$FAKE_STORE/locked" ] || exit 51
    [ "$n" -gt 0 ] || exit 44
    echo $((n - 1)) > "$FAKE_STORE/items" ;;
  find-generic-password) [ "$n" -gt 0 ] || exit 44 ;;
  *) exit 64 ;;
esac
EOF2
cat > "$tmp/bin/secret-tool" <<'EOF2'
#!/usr/bin/env bash
n=$(cat "$FAKE_STORE/items")
case $1 in
  clear) [ ! -f "$FAKE_STORE/locked" ] || exit 1; echo 0 > "$FAKE_STORE/items" ;;
  search) [ "$n" -eq 0 ] || printf '[/org/freedesktop/secrets/collection/login/1]\nlabel = x\n' ;;
  *) exit 64 ;;
esac
EOF2
chmod +x "$tmp/bin/security" "$tmp/bin/secret-tool"
export FAKE_STORE="$tmp/store"
mkdir -p "$FAKE_STORE"
purge() { # purge <os> <items> <locked:yes|no>: purge_secrets' output, items left after
  echo "$2" > "$FAKE_STORE/items"
  rm -f "$FAKE_STORE/locked"
  [ "$3" = no ] || : > "$FAKE_STORE/locked"
  OS=$1 KEYCHAIN_SERVICE=dev.test run purge_secrets 2>&1 || fail "purge_secrets failed on $1"
  echo "left=$(cat "$FAKE_STORE/items")"
}
for os in macos linux; do
  out=$(purge "$os" 3 no)
  case $out in *"Removed the app's secrets (dev.test)"*left=0) ;; *) fail "$os: purge didn't remove the secrets: $out" ;; esac
  out=$(purge "$os" 0 no)
  case $out in *"Removed the app's secrets"*left=0) ;; *) fail "$os: purge with nothing stored: $out" ;; esac
  out=$(purge "$os" 2 yes)
  case $out in *"couldn't be removed"*"locked"*left=2) ;; *) fail "$os: a locked store wasn't reported: $out" ;; esac
done

# The app menu entry, files only, for an app under a home with a space, & and $.
menu_home="$tmp/home & \$dir"
mkdir -p "$menu_home/app/bin" "$menu_home/app/share/acme/tray"
printf '#!/bin/sh\n' > "$menu_home/app/bin/automator-tray"
chmod +x "$menu_home/app/bin/automator-tray"
cp "$repo/scripts/app.desktop.in" "$tmp/"
menu() { # menu <function> [args]: with the installer's names, in that home
  HOME="$menu_home" XDG_DATA_HOME="" OS=linux NAME="Acme Auto" COMMAND=acme FOLDER=acme here="$tmp" \
    PROCAUTO_INSTALL_FILES_ONLY=1 files_only=1 bash -c '. "$1"; shift; "$@"' _ "$repo/scripts/install-app.sh" "$@"
}
menu install_menu_entry "$menu_home/app" >/dev/null || fail "install_menu_entry failed"
entry="$menu_home/.local/share/applications/acme.desktop"
grep -qxF "Exec=\"$tmp/home & \\\\\$dir/app/bin/automator-tray\" --open" "$entry" ||
  fail "the app menu entry's Exec isn't the tray, quoted, with --open: $(grep Exec "$entry")"
grep -qxF "Name=Acme Auto" "$entry" || fail "the app menu entry doesn't name the app"
grep -qxF "Icon=$menu_home/app/share/acme/tray/app.png" "$entry" || fail "the app menu entry's icon: $(grep Icon "$entry")"
grep -qxF "Categories=Utility;" "$entry" || fail "the app menu entry has no category"
menu remove_menu_entry || fail "remove_menu_entry failed"
[ ! -e "$entry" ] || fail "remove_menu_entry left the entry"
menu remove_menu_entry || fail "remove_menu_entry of no entry failed"

echo "--- ok"
