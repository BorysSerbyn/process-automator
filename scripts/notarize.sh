#!/usr/bin/env bash
# Notarizes a Developer ID-signed app with Apple and staples the ticket to it,
# so it opens without Gatekeeper warnings, even offline.
# Usage: notarize.sh <app>
# Needs an App Store Connect API key: APPLE_API_KEY_PATH (the .p8 file),
# APPLE_API_KEY_ID and APPLE_API_ISSUER_ID.
set -euo pipefail
app=${1:?usage: notarize.sh <app>}
: "${APPLE_API_KEY_PATH:?}" "${APPLE_API_KEY_ID:?}" "${APPLE_API_ISSUER_ID:?}"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
ditto -c -k --keepParent "$app" "$work/submit.zip"
xcrun notarytool submit "$work/submit.zip" --key "$APPLE_API_KEY_PATH" --key-id "$APPLE_API_KEY_ID" \
  --issuer "$APPLE_API_ISSUER_ID" --wait --timeout 30m --output-format plist > "$work/result.plist"
status=$(plutil -extract status raw -o - "$work/result.plist")
id=$(plutil -extract id raw -o - "$work/result.plist")
if [ "$status" != Accepted ]; then
  echo "notarization $id: $status" >&2
  xcrun notarytool log "$id" --key "$APPLE_API_KEY_PATH" --key-id "$APPLE_API_KEY_ID" --issuer "$APPLE_API_ISSUER_ID" >&2 || true
  exit 1
fi
xcrun stapler staple "$app"
spctl --assess --type execute --verbose=2 "$app"
