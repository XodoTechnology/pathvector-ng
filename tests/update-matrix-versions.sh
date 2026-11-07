#!/usr/bin/env bash
# Reconcile tests/{bird,frr}-matrix/versions.txt with upstream releases.
#
# For each backend this keeps the matrix current without ballooning it:
#   * the highest tracked major version gets EVERY minor release line
#     (latest stable patch each) — so a new minor like FRR 10.6 is added
#     automatically;
#   * lower tracked majors keep only their already-listed minor lines,
#     bumped to the latest patch — legacy lines stay a curated spot-check;
#   * the lowest version already in versions.txt is kept as a pinned entry
#     even when it isn't its minor's newest patch (BIRD v2.0.7 =
#     supportedMin);
#   * upstream MAJOR versions newer than anything tracked are reported but
#     NOT added — a new major usually needs code review first.
#
# FRR lines that need a source-built libyang carry it as a second field
# ("frr-10.0.5 v2.1.148"); the ref is looked up per minor line so a patch
# bump keeps it.
#
# Usage:
#   tests/update-matrix-versions.sh          # print proposed versions.txt + report
#   tests/update-matrix-versions.sh --check  # exit 1 if versions.txt would change
#   tests/update-matrix-versions.sh --write  # rewrite versions.txt in place
#
# Intended to run in CI on a schedule; see .github/workflows/matrix-versions.yml.
set -euo pipefail
cd "$(dirname "$0")/.."

BIRD_REPO="${BIRD_REPO:-https://github.com/CZ-NIC/bird.git}"
FRR_REPO="${FRR_REPO:-https://github.com/FRRouting/frr.git}"

mode="${1:---check}"
changed=0
report_file="$(mktemp)"
trap 'rm -f "$report_file"' EXIT

# Stable upstream tags for a repo matching an ERE, one per line.
upstream_tags() { # <repo> <tag-ERE>
  git ls-remote --tags "$1" 2>/dev/null \
    | awk '{print $2}' \
    | sed -e 's|refs/tags/||' -e 's|\^{}||' \
    | grep -E "$2" \
    | sort -u
}

# "v2.15.1"/"frr-10.4"/"2.14" -> "maj\tmin\tpatch\ttag". A two-component tag
# is patch 0 but keeps its own spelling. Pre-release suffixes are stripped.
parse_tag() { # <tag>
  local t="$1" maj min pat rest
  t="${t#v}"; t="${t#frr-}"
  IFS='.' read -r maj min pat rest <<<"$t"
  pat="${pat%%[^0-9]*}"
  [[ "$pat" =~ ^[0-9]+$ ]] || pat=0
  printf '%s\t%s\t%s\t%s\n' "$maj" "$min" "$pat" "$1"
}

# stdin: "maj\tmin\tpatch\ttag" lines -> stdout "maj.min\t<latest-tag>".
latest_per_minor() {
  local maj min pat tag key nk
  declare -A best=()
  while IFS=$'\t' read -r maj min pat tag; do
    [[ -n "$maj" ]] || continue
    key="$maj.$min"; nk="$maj.$min.$pat"
    # keep the higher normalized version; store "ver|tag"
    if [[ -z "${best[$key]:-}" ]] || \
       [[ "$(printf '%s\n%s\n' "${best[$key]%%|*}" "$nk" | sort -V | tail -1)" == "$nk" ]]; then
      best[$key]="$nk|$tag"
    fi
  done
  for key in "${!best[@]}"; do printf '%s\t%s\n' "$key" "${best[$key]##*|}"; done
}

# Read current versions.txt data lines into CUR_TAGS; lowest -> MIN_TAG.
read_current() { # <file>
  local f="$1" line s tag
  CUR_TAGS=(); MIN_TAG=""
  while IFS= read -r line || [[ -n "$line" ]]; do
    s="${line%%#*}"; s="$(echo "$s" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')"
    [[ -n "$s" ]] || continue
    tag="${s%%[[:space:]]*}"
    CUR_TAGS+=("$tag")
  done <"$f"
  if ((${#CUR_TAGS[@]})); then
    MIN_TAG="$(printf '%s\n' "${CUR_TAGS[@]}" | sort -V | head -1)"
  fi
}

# Emit a full versions.txt (header comments preserved) to stdout.
# $1=file  $2..=new data lines
render_versions() {
  local f="$1" line s seen=0; shift
  while IFS= read -r line || [[ -n "$line" ]]; do
    s="${line%%#*}"; s="$(echo "$s" | sed -e 's/[[:space:]]*$//')"
    if [[ $seen -eq 0 && -n "$(echo "$s" | tr -d '[:space:]')" ]]; then seen=1; fi
    [[ $seen -eq 0 ]] && printf '%s\n' "$line"
  done <"$f"
  local d; for d in "$@"; do printf '%s\n' "$d"; done
}

# $1=name $2=repo $3=tag-ERE $4=versions-file $5=libyang "maj.min=ref ..." (or "")
process_backend() {
  local name="$1" repo="$2" ere="$3" vf="$4" libspec="$5"

  read_current "$vf"
  ((${#CUR_TAGS[@]})) || { echo "!! $vf: no versions found" >&2; return 1; }
  local -a cur=("${CUR_TAGS[@]}")

  local tmp curmin; tmp="$(mktemp)"; curmin="$(mktemp)"
  trap 'rm -f "$tmp" "$curmin"' RETURN

  # upstream latest tag per minor line
  upstream_tags "$repo" "$ere" | while IFS= read -r tg; do parse_tag "$tg"; done \
    | latest_per_minor | sort -t$'\t' -k1,1V >"$tmp"

  # minors currently tracked -> "maj min" lines
  local t maj min
  for t in "${cur[@]}"; do
    parse_tag "$t" | { IFS=$'\t' read -r maj min _ _; printf '%s %s\n' "$maj" "$min"; }
  done | sort -u >"$curmin"
  local highest_major; highest_major="$(awk '{print $1}' "$curmin" | sort -n | tail -1)"

  # desired data lines
  local -a desired=()
  # pinned minimum: keep lowest current tag if it isn't its minor's latest
  local min_mm latest_for_min
  min_mm="$(parse_tag "$MIN_TAG" | cut -f1,2 | tr '\t' '.')"
  latest_for_min="$(awk -F'\t' -v k="$min_mm" '$1==k{print $2}' "$tmp")"
  if [[ -n "$latest_for_min" && "$MIN_TAG" != "$latest_for_min" ]]; then
    desired+=("$MIN_TAG")
  fi

  # libyang lookup helper: "maj.min" -> ref (always rc 0; set -e safe)
  libyang_ref() { # <maj> <min>
    local kv
    if [[ -n "$libspec" ]]; then
      for kv in $libspec; do
        if [[ "$kv" == "$1.$2="* ]]; then
          echo "${kv#*=}"
          return 0
        fi
      done
    fi
    return 0
  }

  local mm ltag cmaj cmin outline ref tracked
  while IFS=$'\t' read -r mm ltag; do
    [[ -n "$mm" ]] || continue
    cmaj="${mm%%.*}"; cmin="${mm#*.}"
    tracked=0
    if grep -q "^$cmaj $cmin\$" "$curmin"; then tracked=1; fi
    # include if it's the highest tracked major OR an already-tracked minor
    if [[ "$cmaj" -eq "$highest_major" || $tracked -eq 1 ]]; then
      outline="$ltag"; ref="$(libyang_ref "$cmaj" "$cmin")"
      if [[ -n "$ref" ]]; then outline="$ltag $ref"; fi
      desired+=("$outline")
    fi
  done <"$tmp"

  # sort by version, dedup identical lines
  local -a sorted
  mapfile -t sorted < <(printf '%s\n' "${desired[@]}" \
    | awk '{print $1"\t"$0}' | sort -t$'\t' -k1,1V | cut -f2- | awk '!seen[$0]++')

  # --- report ---
  {
    echo "## $name"
    comm -13 <(printf '%s\n' "${cur[@]}" | sort -u) \
            <(printf '%s\n' "${sorted[@]}" | awk '{print $1}' | sort -u) \
      | sed 's/^/+ /; s/$/  (added)/'
    comm -23 <(printf '%s\n' "${cur[@]}" | sort -u) \
            <(printf '%s\n' "${sorted[@]}" | awk '{print $1}' | sort -u) \
      | sed 's/^/- /; s/$/  (removed)/'
    local max_up; max_up="$(cut -f1 "$tmp" | cut -d. -f1 | sort -n | tail -1)"
    if [[ "$max_up" -gt "$highest_major" ]]; then
      echo "!! new upstream major $max_up detected (tracking through $highest_major) — review before adding"
    fi
  } >>"$report_file"

  # --- emit or write ---
  if [[ "$mode" == "--write" ]]; then
    render_versions "$vf" "${sorted[@]}" >"$vf.new" && mv "$vf.new" "$vf"
  elif [[ "$mode" == "--check" ]]; then
    if ! render_versions "$vf" "${sorted[@]}" | diff -q "$vf" - >/dev/null; then
      echo "DRIFT: $vf would change" >>"$report_file"; changed=1
    fi
  else
    echo "### $vf (proposed)"; render_versions "$vf" "${sorted[@]}"
  fi
}

# BIRD: no libyang. FRR: libyang refs for the pre-10.1 lines.
process_backend bird "$BIRD_REPO" '^v[0-9]+(\.[0-9]+){1,2}$' tests/bird-matrix/versions.txt ""
process_backend frr  "$FRR_REPO"  '^frr-[0-9]+\.[0-9]+\.[0-9]+$' tests/frr-matrix/versions.txt \
  "9.1=v2.1.80 10.0=v2.1.148"

echo "=== report ===" >&2
cat "$report_file" >&2
[[ "$changed" -eq 1 ]] && exit 1
exit 0
