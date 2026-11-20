#!/usr/bin/env bash
# Installs or uninstalls the Process Automator this script is part of: the macOS
# app (it sits in Contents/Resources) or the Linux tree (share/process-automator).
# It's the one install path: `make install`, install.sh, `procauto update`,
# `procauto uninstall` and the Homebrew cask and formula all run it.
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
# Starting at login keeps what I chose (`procauto login-items disable`):
# an item turned off is (re)started now and stays off at login. launchd refuses
# to load a disabled agent, so it's enabled for the load and disabled again,
# even when the load fails. It reads `launchctl print-disabled` as
# core/internal/cli/loginitems_darwin.go does; keep the two in step.
set -euo pipefail

main() {
  here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
  root=$(cd "$here/../.." && pwd)
  # VERSION, OS (macos|linux), BUNDLE_ID, UI_LABEL, SCHED_LABEL, KEYCHAIN_SERVICE
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
    app_name="Process Automator.app" core_rel="Contents/MacOS/procauto"
  else
    INSTALL_DIR=${INSTALL_DIR:-$HOME/.local/lib}
    app_name="process-automator" core_rel="bin/procauto"
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
    stop_items
    # Copy beside it, then swap, so a failed copy leaves the installed app whole.
    rm -rf "$dest.new" "$dest.old"
    if [ "$OS" = macos ]; then ditto "$root" "$dest.new"; else cp -R "$root" "$dest.new"; fi
    if [ -e "$dest" ]; then mv "$dest" "$dest.old"; fi
    mv "$dest.new" "$dest"
    rm -rf "$dest.old"
  fi
  if [ -n "$link" ]; then
    mkdir -p "$BIN_DIR"
    ln -sfn "$dest/$core_rel" "$BIN_DIR/procauto"
  fi
  login_items "$dest"
  echo "Installed Process Automator $VERSION: $dest"
  if [ -n "$link" ]; then echo "CLI: $BIN_DIR/procauto"; fi
}

# The scheduler's PATH: launchd's and systemd's defaults lack ~/.local/bin and
# Homebrew (claude, MCP server commands). Commands also add the login shell's.
sched_path() {
  echo "$BIN_DIR:$HOME/.local/bin:/opt/homebrew/bin:/home/linuxbrew/.linuxbrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
}

login_items() {
  local app=$1
  if [ "$OS" = macos ]; then
    local agents="$HOME/Library/LaunchAgents" logs="$HOME/Library/Logs/process-automator"
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
    local units=${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user existed=""
    mkdir -p "$units"
    [ -f "$units/$SCHED_LABEL" ] && existed=1
    sed -e "s|__PROGRAM__|$app/$core_rel|" -e "s|__PATH__|$(sched_path)|" \
      "$here/scheduler.service.in" > "$units/$SCHED_LABEL"
    if system; then
      if ! systemctl --user show-environment >/dev/null 2>&1; then
        echo "No systemd user session here: start the scheduler yourself with \`procauto daemon\`." >&2
        return 0
      fi
      systemctl --user daemon-reload
      # A first install starts at login; a reinstall keeps the choice.
      [ -z "$existed" ] && systemctl --user enable "$SCHED_LABEL"
      systemctl --user restart "$SCHED_LABEL"
    fi
    echo "Login item: $SCHED_LABEL (log: journalctl --user -u $SCHED_LABEL)"
  fi
}

launchd_domain() { echo "gui/$(id -u)"; }
disabled() { launchctl print-disabled "$(launchd_domain)" | grep -Eq "\"$1\" => (disabled|true)"; }

# load_agent <plist> <label>: (re)loads the agent, keeping it disabled at
# login when it was.
load_agent() {
  local plist=$1 label=$2 domain
  domain=$(launchd_domain)
  launchctl bootout "$domain/$label" 2>/dev/null || true
  if disabled "$label"; then
    local status=0
    launchctl enable "$domain/$label"
    launchctl bootstrap "$domain" "$plist" || status=$?
    launchctl disable "$domain/$label"
    return $status
  fi
  launchctl bootstrap "$domain" "$plist"
}

stop_items() {
  system || return 0
  if [ "$OS" = macos ]; then
    launchctl bootout "$(launchd_domain)/$UI_LABEL" 2>/dev/null || true
    launchctl bootout "$(launchd_domain)/$SCHED_LABEL" 2>/dev/null || true
  else
    systemctl --user stop "$SCHED_LABEL" 2>/dev/null || true
  fi
}

uninstall_app() {
  local core="$root/$core_rel"
  [ -x "$core" ] || die "$root isn't an installed Process Automator (no $core_rel): nothing removed"
  stop_items
  if [ "$OS" = macos ]; then
    rm -f "$HOME/Library/LaunchAgents/$UI_LABEL.plist" "$HOME/Library/LaunchAgents/$SCHED_LABEL.plist"
  else
    local units=${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user
    if system; then systemctl --user disable "$SCHED_LABEL" 2>/dev/null || true; fi
    rm -f "$units/$SCHED_LABEL"
    if system; then systemctl --user daemon-reload 2>/dev/null || true; fi
  fi
  if [ -n "$purge" ]; then
    local data
    data=$("$core" ${config:+--config "$config"} paths --json | sed -n 's/^  "data": "\(.*\)",*$/\1/p')
    if [ -n "$data" ] && [ -d "$data" ]; then echo "Removing $data"; rm -rf "$data"; fi
    if [ "$OS" = macos ]; then
      rm -rf "$HOME/Library/Logs/process-automator"
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
  local link="$BIN_DIR/procauto" target
  if [ -L "$link" ]; then
    target=$(readlink "$link")
    if [ "$(cd "$(dirname "$target")" 2>/dev/null && pwd -P)/$(basename "$target")" = "$(cd "$(dirname "$core")" && pwd -P)/$(basename "$core")" ]; then
      rm -f "$link"
    fi
  fi
  rm -rf "$root"
  echo "Uninstalled Process Automator $VERSION."
}

main "$@"
