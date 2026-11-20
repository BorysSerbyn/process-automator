#!/usr/bin/env bash
# Assembles "Process Automator.app": automator-ui as the main executable, the Go core bundled alongside,
# the templates, and the installer with what it needs (install-app.sh, install.env, the LaunchAgent templates).
# Usage: bundle.sh <core-binary> <ui-binary> <out-dir> <version> <bundle-id-prefix> <sign-identity> <install.env>
# SIGN_FLAGS adds codesign flags (releases: --options runtime --timestamp, for notarization).
set -euo pipefail
core=$1 ui=$2 out=$3 version=$4 prefix=$5 identity=$6 env=$7
scripts="$(cd "$(dirname "$0")" && pwd)"
src="$scripts/../automator-ui/Resources"
app="$out/Process Automator.app"
rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp "$ui" "$app/Contents/MacOS/AutomatorUI"
cp "$core" "$app/Contents/MacOS/procauto"
res="$app/Contents/Resources"
cp "$src/Logo/glyph.svg" "$src/Logo/dot.svg" "$src/Logo/AppIcon.icns" "$res/"
# Routine templates, found by the core relative to its binary (version.TemplatesDir).
cp -R "$scripts/../templates" "$res/templates"
cp "$scripts/install-app.sh" "$scripts/launchagent.plist.in" "$scripts/scheduler.plist.in" "$res/"
cp "$env" "$res/install.env"
sed -e "s/__SHORT_VERSION__/${version%%-*}/g" -e "s/__VERSION__/$version/g" -e "s/__BUNDLE_ID__/$prefix.procauto/g" \
  "$src/Info.plist" > "$app/Contents/Info.plist"
# Signing refuses extended attributes (Finder info, quarantine) on what it seals.
xattr -cr "$app"
# Sign inner binary first, then the bundle, with a stable identity so macOS keeps permission grants across rebuilds.
# shellcheck disable=SC2086 # SIGN_FLAGS is a list of flags
codesign --force ${SIGN_FLAGS:-} --sign "$identity" --identifier "$prefix.procauto.core" "$app/Contents/MacOS/procauto"
# shellcheck disable=SC2086
codesign --force ${SIGN_FLAGS:-} --sign "$identity" "$app"
echo "$app"
