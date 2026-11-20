#!/usr/bin/env bash
# Prints the build version from git tags (vMAJOR.MINOR.PATCH).
# Exactly on a tag with a clean tree: "1.2.3". Otherwise: "<next-patch>-dev+<sha>[.dirty]".
# Before the first tag: "0.1.0-dev+<sha>[.dirty]" (the first release is v0.1.0).
set -euo pipefail
first=0.1.0
sha=$(git rev-parse --short HEAD 2>/dev/null || echo unknown)
dirty=""; git diff --quiet HEAD 2>/dev/null || dirty=".dirty"
if ! tag=$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' 2>/dev/null); then
  echo "${first}-dev+${sha}${dirty}"
  exit 0
fi
base=${tag#v}
if [[ $(git describe --tags --exact-match --match "$tag" 2>/dev/null || true) == "$tag" && -z $dirty ]]; then
  echo "$base"
else
  IFS=. read -r major minor patch <<<"$base"
  echo "${major}.${minor}.$((patch + 1))-dev+${sha}${dirty}"
fi
