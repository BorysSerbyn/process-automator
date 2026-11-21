#!/usr/bin/env bash
# Makes a release artifact, named as `update` and install.sh look for it (the
# app's FOLDER, COMMAND and VERSION come from its install.env).
# Usage:
#   package.sh macos <app> <out-dir>
#       → <out-dir>/<FOLDER>-<version>-macos.zip (the signed .app, zipped with ditto)
#   package.sh linux <core-binary> <out-dir> <arch> <install.env> <tray-binary> <tray-icons>
#       → <out-dir>/<FOLDER>-<version>-linux-<arch>.tar.gz, holding bin/<COMMAND>,
#         bin/automator-tray (the tray, beside the core) and share/<FOLDER>/
#         (templates, installer, systemd unit and autostart entry templates,
#         the tray's icons in tray/)
# TEMPLATES is the templates folder the Linux tree ships (default: the repo's templates/).
set -euo pipefail
scripts="$(cd "$(dirname "$0")" && pwd)"
kind=${1:?usage: package.sh macos|linux ...}
shift

env_var() { sed -n "s/^$2=//p" "$1" | tr -d '"'; }

case $kind in
  macos)
    app=$1 out=$2
    env="$app/Contents/Resources/install.env"
    version=$(env_var "$env" VERSION) folder=$(env_var "$env" FOLDER)
    mkdir -p "$out"
    zip="$out/$folder-$version-macos.zip"
    rm -f "$zip"
    ditto -c -k --keepParent "$app" "$zip"
    echo "$zip"
    ;;
  linux)
    core=$1 out=$2 arch=$3 env=$4 tray=$5 icons=$6
    version=$(env_var "$env" VERSION) folder=$(env_var "$env" FOLDER) command=$(env_var "$env" COMMAND)
    templates=${TEMPLATES:-$scripts/../templates}
    name="$folder-$version-linux-$arch"
    stage=$(mktemp -d)
    trap 'rm -rf "$stage"' EXIT
    tree="$stage/$name"
    mkdir -p "$tree/bin" "$tree/share/$folder"
    cp "$core" "$tree/bin/$command"
    cp "$tray" "$tree/bin/automator-tray"
    chmod 755 "$tree/bin/$command" "$tree/bin/automator-tray"
    cp -R "$icons" "$tree/share/$folder/tray"
    if [ -d "$templates" ]; then cp -R "$templates" "$tree/share/$folder/templates"; else mkdir "$tree/share/$folder/templates"; fi
    cp "$scripts/install-app.sh" "$scripts/scheduler.service.in" "$scripts/tray.desktop.in" "$tree/share/$folder/"
    cp "$env" "$tree/share/$folder/install.env"
    mkdir -p "$out"
    out=$(cd "$out" && pwd)
    # No macOS extended attributes (AppleDouble ._ files) when packaged on a Mac.
    (cd "$stage" && COPYFILE_DISABLE=1 tar --no-xattrs -czf "$out/$name.tar.gz" "$name")
    echo "$out/$name.tar.gz"
    ;;
  *) echo "package.sh: unknown kind $kind" >&2; exit 1 ;;
esac
