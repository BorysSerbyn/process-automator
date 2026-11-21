#!/usr/bin/env bash
# Tests install-app.sh's LaunchAgent handling (stop_agent, load_agent,
# disabled) under its own `set -euo pipefail`, against a fake launchctl that
# behaves like the real one: `print` of an agent that isn't loaded exits 113.
# An update stops the agents, then (re)loads each one: stopping an agent that
# isn't loaded must not end the script. Runs anywhere (no real launchd).
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

echo "--- ok"
