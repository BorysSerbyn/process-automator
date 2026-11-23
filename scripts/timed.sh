#!/usr/bin/env bash
# Times the build's steps, so it's known where make ci's and make publish's
# time goes.
#   timed.sh <label> <command>   runs the shell command, says how long it
#                                took, and adds a line (seconds, outcome,
#                                label) to the file $TIMINGS names, if set;
#                                exits as the command did
#   timed.sh --summary <file>    prints the steps recorded there, longest
#                                first, with their total
# A label starting with "· " is part of another step (timed inside it): it's
# listed, and left out of the total.
set -uo pipefail

now() { perl -MTime::HiRes=time -e 'printf "%.1f\n", time'; }

if [[ ${1:-} == --summary ]]; then
  file=${2:?usage: timed.sh --summary <file>}
  [[ -s $file ]] || exit 0
  echo "--- timings ($file)"
  sort -t$'\t' -k1,1 -rn "$file" | awk -F'\t' '{ printf "%8.1fs  %-6s %s\n", $1, $2, $3; if ($3 !~ /^· /) total += $1 }
    END { printf "%8.1fs  total\n", total }'
  exit 0
fi

label=${1:?usage: timed.sh <label> <command>}
cmd=${2:?usage: timed.sh <label> <command>}
start=$(now)
bash -c "$cmd"
status=$?
secs=$(awk -v a="$start" -v b="$(now)" 'BEGIN { printf "%.1f", b - a }')
outcome=ok
[[ $status -eq 0 ]] || outcome=failed
echo "timed: $label: ${secs}s ($outcome)"
if [[ -n ${TIMINGS:-} ]]; then
  mkdir -p "$(dirname "$TIMINGS")"
  printf '%s\t%s\t%s\n' "$secs" "$outcome" "$label" >> "$TIMINGS"
fi
exit "$status"
