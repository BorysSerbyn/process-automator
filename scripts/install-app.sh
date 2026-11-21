#!/usr/bin/env bash
# Installs or uninstalls the app this script is part of: the macOS app (it
# sits in Contents/Resources) or the Linux tree (share/<FOLDER>). Its
# install.env names the app (NAME, COMMAND, FOLDER) and its build.
# It's the one install path: `make install`, install.sh, `<COMMAND> update`,
# `<COMMAND> uninstall` and the Homebrew cask and formula all run it.
#
# Usage:
#   install-app.sh install [--to <path>]
#       Copies this app into place, links the CLI into $BIN_DIR and sets up and
#       (re)starts the login items. --to replaces the app at <path> instead
#       (`update`), leaving the CLI link as it is.
#   install-app.sh login-items
#       Sets up and (re)starts the login items for this app where it is (Homebrew).
#   install-app.sh uninstall [--purge] [--config <file>]
#       Stops and removes the login items, the CLI link and this app; --purge
#       also the config, data (routines, history), logs and secrets.
# Options:
#   --files-only (or PROCAUTO_INSTALL_FILES_ONLY=1): only files; never touches
#       launchd, systemd or the keychain (tests, packaging).
# Environment: INSTALL_DIR (macOS: ~/Applications; Linux: ~/.local/lib) and
#   BIN_DIR (~/.local/bin).
#
# Starting at login keeps what I chose (`<COMMAND> login-items disable`):
# an item turned off is (re)started now and stays off at login. launchd refuses
# to load a disabled agent, so it's enabled for the load and disabled again,
# even when the load fails. It reads `launchctl print-disabled` as
# core/internal/cli/loginitems_darwin.go does; keep the two in step.
#
# On Linux the tray (bin/automator-tray) starts at login from an XDG autostart
# entry, in graphical sessions only; it's turned off at login by hiding the
# entry (Hidden=true), as core/internal/cli/autostart.go reads and writes it.
# A running tray is stopped (and waited for) before its files are replaced,
# and started again in the session it ran in; with none running, it's started
# only when this runs in a graphical session. A headless machine never starts it.
set -euo pipefail

main() {
  here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
  root=$(cd "$here/../.." && pwd)
  # OS (macos|linux), NAME, COMMAND, FOLDER, VERSION, BUNDLE_ID, UI_LABEL,
  # SCHED_LABEL, KEYCHAIN_SERVICE
  # shellcheck source=/dev/null
  . "$here/install.env"

  local action=${1:-}
  [ $# -gt 0 ] && shift
  to="" purge="" config="" files_only=${PROCAUTO_INSTALL_FILES_ONLY:-}
  while [ $# -gt 0 ]; do
    case $1 in
      --to) to=$2; shift 2 ;;
      --purge) purge=1; shift ;;
      --config) config=$2; shift 2 ;;
      --files-only) files_only=1; shift ;;
      *) die "unknown option $1" ;;
    esac
  done

  BIN_DIR=${BIN_DIR:-$HOME/.local/bin}
  if [ "$OS" = macos ]; then
    INSTALL_DIR=${INSTALL_DIR:-$HOME/Applications}
    app_name="$NAME.app" core_rel="Contents/MacOS/$COMMAND"
  else
    INSTALL_DIR=${INSTALL_DIR:-$HOME/.local/lib}
    app_name="$FOLDER" core_rel="bin/$COMMAND"
  fi

  case $action in
    install) install_app ;;
    login-items) login_items "$root" ;;
    uninstall) uninstall_app ;;
    *) die "usage: install-app.sh install [--to <path>] | login-items | uninstall [--purge] [--config <file>]" ;;
  esac
}

die() { echo "install-app.sh: $*" >&2; exit 1; }
system() { [ -z "$files_only" ]; }

install_app() {
  local dest link=1
  if [ -n "$to" ]; then
    dest=$to link=""
  else
    dest="$INSTALL_DIR/$app_name"
  fi
  mkdir -p "$(dirname "$dest")"
  if [ "$root" != "$dest" ]; then
    stop_items "$dest"
    # Copy beside it, then swap, so a failed copy leaves the installed app
    # whole; and running again, since it was just stopped.
    rm -rf "$dest.new" "$dest.old"
    if ! copy_app "$root" "$dest.new" || ! swap_in "$dest"; then
      rm -rf "$dest.new"
      if [ -x "$dest/$core_rel" ]; then login_items "$dest" >/dev/null || true; fi
      die "couldn't put $NAME $VERSION in place at $dest: the app there is left as it was"
    fi
    rm -rf "$dest.old"
  fi
  if [ -n "$link" ]; then
    mkdir -p "$BIN_DIR"
    ln -sfn "$dest/$core_rel" "$BIN_DIR/$COMMAND"
  fi
  login_items "$dest"
  echo "Installed $NAME $VERSION: $dest"
  if [ -n "$link" ]; then echo "CLI: $BIN_DIR/$COMMAND"; fi
}

# copy_app <from> <to>: a copy of the app, as each OS's tools make one.
copy_app() {
  if [ "$OS" = macos ]; then ditto "$1" "$2"; else cp -R "$1" "$2"; fi
}

# swap_in <dest>: <dest>.new in place of <dest> (kept as <dest>.old), or,
# when that fails, <dest> as it was.
swap_in() {
  local dest=$1
  if [ -e "$dest" ]; then mv "$dest" "$dest.old" || return 1; fi
  mv "$dest.new" "$dest" && return 0
  if [ -e "$dest.old" ]; then mv "$dest.old" "$dest"; fi
  return 1
}

# The scheduler's PATH: launchd's and systemd's defaults lack ~/.local/bin and
# Homebrew (claude, MCP server commands). Commands also add the login shell's.
sched_path() {
  echo "$BIN_DIR:$HOME/.local/bin:/opt/homebrew/bin:/home/linuxbrew/.linuxbrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
}

login_items() {
  local app=$1
  if [ "$OS" = macos ]; then
    local agents="$HOME/Library/LaunchAgents" logs="$HOME/Library/Logs/$FOLDER"
    mkdir -p "$agents" "$logs"
    sed -e "s|__LABEL__|$UI_LABEL|" -e "s|__PROGRAM__|$app/Contents/MacOS/AutomatorUI|" \
      "$here/launchagent.plist.in" > "$agents/$UI_LABEL.plist"
    sed -e "s|__LABEL__|$SCHED_LABEL|" -e "s|__PROGRAM__|$app/$core_rel|" \
      -e "s|__PATH__|$(sched_path)|" -e "s|__LOG__|$logs/scheduler.log|" \
      "$here/scheduler.plist.in" > "$agents/$SCHED_LABEL.plist"
    if system; then
      load_agent "$agents/$UI_LABEL.plist" "$UI_LABEL"
      load_agent "$agents/$SCHED_LABEL.plist" "$SCHED_LABEL"
    fi
    echo "Login items: $UI_LABEL, $SCHED_LABEL (scheduler log: $logs/scheduler.log)"
  else
    install_tray "$app"
    install_scheduler "$app"
  fi
}

# sed_value <text>: text as a sed replacement (delimiter |).
sed_value() { printf '%s' "$1" | sed -e 's/[\\|&]/\\&/g'; }

# unit_quoted <text> [exec]: text as one double-quoted word of a systemd unit
# setting (spaces and quotes kept, % not a specifier; in ExecStart, $ not a
# variable either).
unit_quoted() {
  local v=${1//\\/\\\\}
  v=${v//\"/\\\"} v=${v//%/%%}
  if [ -n "${2:-}" ]; then v=${v//\$/\$\$}; fi
  printf '"%s"' "$v"
}

# user_session: systemd's user manager is reachable (a login session, or lingering).
user_session() { systemctl --user show-environment >/dev/null 2>&1; }

# manager_config: the XDG_CONFIG_HOME systemd's user manager has (empty when
# it has none, or none it says plainly), which may not be this shell's.
manager_config() {
  local v
  v=$(systemctl --user show-environment 2>/dev/null | sed -n 's/^XDG_CONFIG_HOME=//p')
  case $v in "\$'"*) v="" ;; esac
  printf '%s' "$v"
}

# units_dir: where systemd's user manager reads this user's units: its
# XDG_CONFIG_HOME, not necessarily this shell's (files only: this shell's).
units_dir() {
  local conf=${XDG_CONFIG_HOME:-}
  if system && user_session; then conf=$(manager_config); fi
  echo "${conf:-$HOME/.config}/systemd/user"
}

install_scheduler() {
  local app=$1 units existed=""
  units=$(units_dir)
  mkdir -p "$units"
  [ -f "$units/$SCHED_LABEL" ] && existed=1
  sed -e "s|__NAME__|$(sed_value "${NAME//%/%%}")|" \
    -e "s|__PROGRAM__|$(sed_value "$(unit_quoted "$app/$core_rel" exec)")|" \
    -e "s|__PATH__|$(sed_value "$(unit_quoted "PATH=$(sched_path)")")|" \
    "$here/scheduler.service.in" > "$units/$SCHED_LABEL"
  if system; then
    if ! user_session; then
      echo "No systemd user session here: start the scheduler yourself with \`$COMMAND daemon\`." >&2
      return 0
    fi
    systemctl --user daemon-reload
    # A first install starts at login; a reinstall keeps the choice.
    [ -z "$existed" ] && systemctl --user enable "$SCHED_LABEL"
    systemctl --user restart "$SCHED_LABEL"
    scheduler_notes
  fi
  echo "Login item: $SCHED_LABEL (log: journalctl --user -u $SCHED_LABEL)"
}

# scheduler_notes: what keeps the scheduler from doing what this shell's
# commands expect: a config folder of its own, or stopping at logout.
scheduler_notes() {
  local conf
  conf=$(manager_config)
  if [ "${XDG_CONFIG_HOME:-$HOME/.config}" != "${conf:-$HOME/.config}" ]; then
    echo "Note: XDG_CONFIG_HOME is ${XDG_CONFIG_HOME:-unset} here but ${conf:-unset} for systemd's user session," \
      "so the scheduler would read another config folder than \`$COMMAND\` here. Set it the same for both" \
      "(e.g. in ~/.config/environment.d/), log in again, and reinstall." >&2
  fi
  if ! graphical_session && [ "$(loginctl show-user "$(id -un)" -p Linger --value 2>/dev/null)" = no ]; then
    echo "Note: the scheduler runs only while you're logged in. To run routines when you're not," \
      "keep your user session running: \`sudo loginctl enable-linger $(id -un)\`." >&2
  fi
}

# --- The Linux tray ---------------------------------------------------------

autostart_entry() { echo "${XDG_CONFIG_HOME:-$HOME/.config}/autostart/$UI_LABEL"; }

# install_tray <app>: the tray's autostart entry, and the tray (re)started.
install_tray() {
  local app=$1 exe entry hidden=""
  exe="$app/bin/automator-tray" entry=$(autostart_entry)
  [ -n "$UI_LABEL" ] && [ -x "$exe" ] || return 0
  mkdir -p "$(dirname "$entry")"
  # A reinstall keeps the choice (`login-items disable` hides the entry).
  if [ -f "$entry" ] && grep -Eq '^[[:space:]]*(Hidden[[:space:]]*=[[:space:]]*true|X-GNOME-Autostart-enabled[[:space:]]*=[[:space:]]*false)' "$entry"; then
    hidden=1
  fi
  sed -e "s|__NAME__|$(sed_value "$NAME")|g" -e "s|__PROGRAM__|$(sed_value "$(desktop_quoted "$exe")")|" \
    -e "s|__ICON__|$(sed_value "$app/share/$FOLDER/tray/app.png")|" \
    "$here/tray.desktop.in" > "$entry"
  [ -z "$hidden" ] || echo "Hidden=true" >> "$entry"
  local started=""
  if system; then
    stop_tray "$exe"
    start_tray "$exe" && started=1
  fi
  echo "Login item: $UI_LABEL (the tray, in desktop sessions${started:+; started})"
}

# desktop_quoted <path>: path as a desktop entry's Exec argument: quoted,
# with \ " ` $ escaped inside the quotes and % doubled (the Exec key's rules,
# then the string type's: each backslash written twice).
desktop_quoted() {
  local v=${1//\\/\\\\\\\\}
  v=${v//\"/\\\\\"} v=${v//\`/\\\\\`} v=${v//\$/\\\\\$} v=${v//%/%%}
  printf '"%s"' "$v"
}

# tray_pids <exe>: this user's processes running the tray at <exe> (others'
# /proc/<pid>/exe can't be read).
tray_pids() {
  local exe p target
  exe="$(cd "$(dirname "$1")" 2>/dev/null && pwd -P)/$(basename "$1")"
  for p in /proc/[0-9]*; do
    target=$(readlink "$p/exe" 2>/dev/null) || continue
    [ "${target% (deleted)}" = "$exe" ] && echo "${p#/proc/}"
  done
  return 0
}

# The environment the tray last stopped ran in (its session's display and bus).
tray_env=()

# stop_tray <exe>: stops the tray running at <exe> and waits (up to 10s, then
# kills it) until it has exited, so the next one doesn't start beside it.
stop_tray() {
  local exe=$1 pid pids
  pids=$(tray_pids "$exe")
  [ -n "$pids" ] || return 0
  for pid in $pids; do
    if [ ${#tray_env[@]} -eq 0 ]; then
      mapfile -d '' tray_env < "/proc/$pid/environ" 2>/dev/null || tray_env=()
    fi
    kill "$pid" 2>/dev/null || true
  done
  for _ in $(seq 1 20); do
    [ -n "$(tray_pids "$exe")" ] || return 0
    sleep 0.5
  done
  for pid in $(tray_pids "$exe"); do kill -9 "$pid" 2>/dev/null || true; done
}

# graphical_session: this runs in a desktop session (a display and a session bus).
graphical_session() {
  [ -n "${WAYLAND_DISPLAY:-}${DISPLAY:-}" ] &&
    { [ -n "${DBUS_SESSION_BUS_ADDRESS:-}" ] || [ -S "${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/bus" ]; }
}

# start_tray <exe>: starts the tray in the session the one stopped ran in,
# else in this one if it's graphical; fails when there's none.
start_tray() {
  local exe=$1
  if [ ${#tray_env[@]} -gt 0 ]; then
    setsid env -i "${tray_env[@]}" "$exe" </dev/null >/dev/null 2>&1 &
  elif graphical_session; then
    setsid "$exe" </dev/null >/dev/null 2>&1 &
  else
    return 1
  fi
}

launchd_domain() { echo "gui/$(id -u)"; }
disabled() { launchctl print-disabled "$(launchd_domain)" | grep -Eq "\"$1\" => (disabled|true)"; }

# stop_agent <label>: boots the agent out and waits (up to a minute) for its
# process to exit. bootout returns while it's still stopping (the scheduler
# waits up to 30s for its runs), and loading the agent again before it has
# exited fails ("Bootstrap failed: 5: Input/output error").
stop_agent() {
  local job pid
  job="$(launchd_domain)/$1"
  pid=$(launchctl print "$job" 2>/dev/null | sed -n 's/^[[:space:]]*pid = \([0-9][0-9]*\)$/\1/p' | head -n 1)
  launchctl bootout "$job" 2>/dev/null || true
  [ -n "$pid" ] || return 0
  for _ in $(seq 1 60); do
    kill -0 "$pid" 2>/dev/null || return 0
    sleep 1
  done
}

# bootstrap <domain> <plist>: loads the agent, trying again for a few seconds
# while launchd is still letting go of the one before; the last try's error
# is shown.
bootstrap() {
  for _ in 1 2 3 4 5; do
    launchctl bootstrap "$1" "$2" 2>/dev/null && return 0
    sleep 2
  done
  launchctl bootstrap "$1" "$2"
}

# load_agent <plist> <label>: (re)loads the agent, keeping it disabled at
# login when it was.
load_agent() {
  local plist=$1 label=$2 domain
  domain=$(launchd_domain)
  stop_agent "$label"
  if disabled "$label"; then
    local status=0
    launchctl enable "$domain/$label"
    bootstrap "$domain" "$plist" || status=$?
    launchctl disable "$domain/$label"
    return $status
  fi
  bootstrap "$domain" "$plist"
}

# stop_items <app>: stops the login items running the app at <app>.
stop_items() {
  system || return 0
  if [ "$OS" = macos ]; then
    stop_agent "$UI_LABEL"
    stop_agent "$SCHED_LABEL"
  else
    stop_tray "$1/bin/automator-tray"
    systemctl --user stop "$SCHED_LABEL" 2>/dev/null || true
  fi
}

uninstall_app() {
  local core="$root/$core_rel"
  [ -x "$core" ] || die "$root isn't an installed $NAME (no $core_rel): nothing removed"
  stop_items "$root"
  if [ "$OS" = macos ]; then
    rm -f "$HOME/Library/LaunchAgents/$UI_LABEL.plist" "$HOME/Library/LaunchAgents/$SCHED_LABEL.plist"
  else
    if system; then systemctl --user disable "$SCHED_LABEL" 2>/dev/null || true; fi
    rm -f "$(units_dir)/$SCHED_LABEL"
    [ -z "$UI_LABEL" ] || rm -f "$(autostart_entry)"
    if system; then systemctl --user daemon-reload 2>/dev/null || true; fi
  fi
  if [ -n "$purge" ]; then
    local data
    data=$("$core" ${config:+--config "$config"} paths --json | sed -n 's/^  "data": "\(.*\)",*$/\1/p')
    if [ -n "$data" ] && [ -d "$data" ]; then echo "Removing $data"; rm -rf "$data"; fi
    if [ "$OS" = macos ]; then
      rm -rf "$HOME/Library/Logs/$FOLDER"
      if system; then
        local n=0
        while security delete-generic-password -s "$KEYCHAIN_SERVICE" >/dev/null 2>&1; do n=$((n + 1)); done
        echo "Removed $n Keychain items ($KEYCHAIN_SERVICE)"
      fi
    elif system && command -v secret-tool >/dev/null; then
      secret-tool clear service "$KEYCHAIN_SERVICE" 2>/dev/null || true
      echo "Removed the keyring items ($KEYCHAIN_SERVICE)"
    fi
  fi
  # The CLI link, only if it's ours (compared resolved: /var is /private/var on macOS).
  local link="$BIN_DIR/$COMMAND" target
  if [ -L "$link" ]; then
    target=$(readlink "$link")
    if [ "$(cd "$(dirname "$target")" 2>/dev/null && pwd -P)/$(basename "$target")" = "$(cd "$(dirname "$core")" && pwd -P)/$(basename "$core")" ]; then
      rm -f "$link"
    fi
  fi
  rm -rf "$root"
  echo "Uninstalled $NAME $VERSION."
}

main "$@"
