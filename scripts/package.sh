#!/usr/bin/env bash
# Makes a release artifact, named as `procauto update` and install.sh look for it.
# Usage:
#   package.sh macos <app> <out-dir>
#       → <out-dir>/process-automator-<version>-macos.zip (the signed .app, zipped with ditto)
#   package.sh linux <core-binary> <out-dir> <arch> <install.env>
#       → <out-dir>/process-automator-<version>-linux-<arch>.tar.gz, holding
#         bin/procauto and share/process-automator/ (templates, installer, systemd unit template)
# The version comes from the app's install.env.
set -euo pipefail
scripts="$(cd "$(dirname "$0")" && pwd)"
kind=${1:?usage: package.sh macos|linux ...}
shift

version_of() { sed -n 's/^VERSION=//p' "$1"; }

case $kind in
  macos)
    app=$1 out=$2
    version=$(version_of "$app/Contents/Resources/install.env")
    mkdir -p "$out"
    zip="$out/process-automator-$version-macos.zip"
    rm -f "$zip"
    ditto -c -k --keepParent "$app" "$zip"
    echo "$zip"
    ;;
  linux)
    core=$1 out=$2 arch=$3 env=$4
    version=$(version_of "$env")
    name="process-automator-$version-linux-$arch"
    stage=$(mktemp -d)
    trap 'rm -rf "$stage"' EXIT
    tree="$stage/$name"
    mkdir -p "$tree/bin" "$tree/share/process-automator"
    cp "$core" "$tree/bin/procauto"
    chmod 755 "$tree/bin/procauto"
    cp -R "$scripts/../templates" "$tree/share/process-automator/templates"
    cp "$scripts/install-app.sh" "$scripts/scheduler.service.in" "$tree/share/process-automator/"
    cp "$env" "$tree/share/process-automator/install.env"
    mkdir -p "$out"
    out=$(cd "$out" && pwd)
    # No macOS extended attributes (AppleDouble ._ files) when packaged on a Mac.
    (cd "$stage" && COPYFILE_DISABLE=1 tar --no-xattrs -czf "$out/$name.tar.gz" "$name")
    echo "$out/$name.tar.gz"
    ;;
  *) echo "package.sh: unknown kind $kind" >&2; exit 1 ;;
esac
