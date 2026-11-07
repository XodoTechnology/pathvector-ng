#!/usr/bin/env bash
# Regenerate the "Verified versions" column of the README daemon-
# compatibility table from the test-matrix version files, so the README
# can't drift from what CI actually exercises.
#
# Usage: tests/compat-report.sh
# Prints a markdown table to stdout.
set -euo pipefail
cd "$(dirname "$0")/.."

# versions.txt lines are "TAG" (BIRD: vX.Y.Z) or "TAG [LIBYANG_REF]" (FRR:
# frr-X.Y.Z). Strip comments/blanks, then the optional second field.
read_versions() { # <file> -> one version per line
  sed -e 's/#.*//' -e 's/[[:space:]]*$//' "$1" \
    | awk 'NF {print $1}' | grep . || true
}

range_of() { # <sorted version lines...> -> "min – max (N releases)"
  local min max n
  min=$(printf '%s\n' "$@" | sort -V | head -1)
  max=$(printf '%s\n' "$@" | sort -V | tail -1)
  n=$(printf '%s\n' "$@" | grep -c .)
  printf '%s – %s (%s release%s)' "$min" "$max" "$n" "$([ "$n" = 1 ] && echo '' || echo 's')"
}

bird=($(read_versions tests/bird-matrix/versions.txt))
frr=($(read_versions tests/frr-matrix/versions.txt))

echo "| Backend | Daemon | Verified versions | Result |"
echo "|---------|--------|-------------------|--------|"
printf '| `bird` | BIRD | %s | all pass — one known XFAIL (v2.0.7 + complex config) |\n' "$(range_of "${bird[@]}")"
printf '| `frr`  | FRR  | %s | core pass — FRR ≤ 10.0 needs `rpki-enable: false` |\n' "$(range_of "${frr[@]}")"
echo '| `cairn` | Cairn | 0.1.1 | e2e pass (private beta) |'
