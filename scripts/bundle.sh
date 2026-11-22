#!/usr/bin/env bash
# Assembles "<NAME>.app": automator-ui as the main executable, the Go core bundled alongside as <COMMAND>,
# the templates, the logo, and the installer with what it needs (install-app.sh, install.env, the LaunchAgent templates).
# Usage: bundle.sh <core-binary> <ui-binary> <out-dir> <sign-identity> <install.env>
# install.env names the app and its build (NAME, COMMAND, VERSION, BUNDLE_ID).
# TEMPLATES (default: the repo's templates/) and LOGO (default: automator-ui/Resources/Logo, holding
# AppIcon.icns, glyph.svg and dot.svg) are the app's own.
# SIGN_FLAGS adds codesign flags (releases: --options runtime --timestamp, for notarization).
set -euo pipefail
core=$1 ui=$2 out=$3 identity=$4 env=$5
scripts="$(cd "$(dirname "$0")" && pwd)"
src="$scripts/../automator-ui/Resources"
templates=${TEMPLATES:-$scripts/../templates} logo=${LOGO:-$src/Logo}
# NAME, COMMAND, VERSION, BUNDLE_ID, …
# shellcheck source=/dev/null
. "$env"
app="$out/$NAME.app"
rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp "$ui" "$app/Contents/MacOS/AutomatorUI"
cp "$core" "$app/Contents/MacOS/$COMMAND"
res="$app/Contents/Resources"
cp "$logo/glyph.svg" "$logo/dot.svg" "$logo/AppIcon.icns" "$res/"
# Routine templates, found by the core relative to its binary (the automator package's templatesDir).
if [ -d "$templates" ]; then cp -R "$templates" "$res/templates"; else mkdir "$res/templates"; fi
cp "$scripts/install-app.sh" "$scripts/launchagent.plist.in" "$scripts/scheduler.plist.in" "$res/"
cp "$env" "$res/install.env"
sed -e "s|__SHORT_VERSION__|${VERSION%%-*}|g" -e "s|__VERSION__|$VERSION|g" -e "s|__BUNDLE_ID__|$BUNDLE_ID|g" \
  -e "s|__NAME__|$NAME|g" -e "s|__COMMAND__|$COMMAND|g" "$src/Info.plist" > "$app/Contents/Info.plist"
# Signing refuses extended attributes (Finder info, quarantine) on what it seals.
xattr -cr "$app"
# Sign inner binary first, then the bundle, with a stable identity so macOS keeps permission grants across rebuilds.
# The core is signed as the app (its bundle ID): macOS attributes the notify plugin's notifications by the
# caller's signing identifier, so the core shares the app's permission to notify.
# shellcheck disable=SC2086 # SIGN_FLAGS is a list of flags
# Both carry the entitlements (scripts/entitlements.plist) that let the hardened runtime ask for calendars,
# contacts, the camera, the microphone and Apple Events, matching Info.plist's usage descriptions; without
# them macOS refuses those outright. Nothing is asked for until something turned on needs it and I grant it
# (permissions request). The file can't hold comments: codesign's parser rejects them.
codesign --force ${SIGN_FLAGS:-} --entitlements "$scripts/entitlements.plist" --sign "$identity" --identifier "$BUNDLE_ID" \
  "$app/Contents/MacOS/$COMMAND"
# shellcheck disable=SC2086
codesign --force ${SIGN_FLAGS:-} --entitlements "$scripts/entitlements.plist" --sign "$identity" "$app"
echo "$app"
