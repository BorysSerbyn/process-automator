#!/usr/bin/env bash
# Prints the notes of release <version> (X.Y.Z), run from the app's repository:
# the file $RELEASE_NOTES if set; else the version's line in docs/Progress.md
# ("- X.Y.Z, on <engine> A.B.C: first; second"), its parts as bullets; else
# the subjects of the commits since the previous release tag.
# Usage: release-notes.sh <version>
set -euo pipefail
version=${1:?usage: release-notes.sh <version>}
if [[ -n ${RELEASE_NOTES:-} ]]; then
  cat "$RELEASE_NOTES"
  exit 0
fi
line=""
if [[ -f docs/Progress.md ]]; then
  line=$(grep -F -- "- $version, " docs/Progress.md | head -1 || true)
fi
if [[ -n $line ]]; then
  rest=${line#"- $version, "}
  head=${rest%%: *}  # e.g. "on process-automator 3.9.0"
  body=${rest#*: }
  [[ $head != "$rest" ]] || { head=""; body=$rest; }
  case $head in
    "") ;;
    on\ *) printf 'Built %s.\n\n' "$head" ;;
    *) printf '%s.\n\n' "$head" ;;
  esac
  # Split on "; " outside backticks and brackets: good enough for prose lines.
  awk -v s="$body" 'BEGIN {
    n = split(s, parts, /; /)
    for (i = 1; i <= n; i++) print "- " toupper(substr(parts[i],1,1)) substr(parts[i],2)
  }'
  exit 0
fi
prev=$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' "v$version^" 2>/dev/null || true)
range=${prev:+$prev..}v$version
git log --no-merges --format='- %s' "$range" 2>/dev/null || git log --no-merges --format='- %s' -20
