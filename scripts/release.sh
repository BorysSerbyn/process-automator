#!/usr/bin/env bash
# Usage: scripts/release.sh major|minor|patch — bumps the latest vX.Y.Z tag and creates the new tag.
# The first release (no tag yet) is v0.1.0, whatever the part.
set -euo pipefail
part=${1:?usage: release.sh major|minor|patch}
[[ -z $(git status --porcelain) ]] || { echo "release: working tree not clean" >&2; exit 1; }
case $part in major|minor|patch) ;; *) echo "release: unknown part '$part'" >&2; exit 1 ;; esac
if tag=$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' 2>/dev/null); then
  IFS=. read -r major minor patch <<<"${tag#v}"
  case $part in
    major) major=$((major + 1)); minor=0; patch=0 ;;
    minor) minor=$((minor + 1)); patch=0 ;;
    patch) patch=$((patch + 1)) ;;
  esac
  new="v${major}.${minor}.${patch}"
else
  new="v0.1.0"
fi
git tag -a "$new" -m "Release $new"
echo "$new (publish it with: make publish, which pushes the tag once it has built and tested the release)"
