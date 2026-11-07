#!/usr/bin/env bash
# Run the Pathvector generate/validate tests against one or more FRR builds.
#
# Usage:
#   tests/frr-matrix/run-tests.sh FRR_ROOT [FRR_ROOT...]
#
# Each FRR_ROOT is a DESTDIR-style install tree produced by
# build-frr-versions.sh (containing usr/bin/vtysh, usr/lib/frr/{zebra,bgpd,
# staticd}, usr/lib/libfrr*, etc/frr, ...). Binaries are located under the
# root and run with LD_LIBRARY_PATH pointed at the root's libraries; the
# compiled-in config/socket directories (/etc/frr, /var/run/frr) are shared
# and are created here if missing. Versions are tested one at a time and
# their daemons are stopped afterwards, so sharing those directories is safe.
#
# For every FRR root and every test config (tests/generate-*.yml) this:
#   1. runs `pathvector generate --dry-run`, which renders frr.conf into the
#      cache directory and validates it with `vtysh -C -f` (pkg/frr.Check),
#   2. re-runs `vtysh -C -f` on the generated config directly, so a parse
#      failure is reported with FRR's own error message, and
#   3. (DAEMON_TEST=1 only) starts zebra/bgpd/staticd, pushes the integrated
#      config with `vtysh -b`, and checks the BGP session table via vtysh.
#
# Environment:
#   PATHVECTOR   pathvector binary to test (default: build one with `go build`)
#   CONFIGS      space separated list of configs (default: tests/generate-*.yml)
#   DAEMON_TEST  set to 1 to also run step 3 (needs root, used in Docker)
#   KEEP_WORK    set to 1 to keep the temporary work directory for debugging
#   XFAIL_FILE   known failures (default: tests/frr-matrix/xfail.txt)
#
# Known incompatibilities are listed in xfail.txt as "<version> <config>".
# A listed combination that fails is reported as XFAIL and does not fail the
# run; one that unexpectedly passes is reported as XPASS and does fail it, so
# the list is kept accurate.

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_dir="$(cd "$script_dir/../.." && pwd)"

if [ $# -eq 0 ]; then
  sed -n '2,33p' "$0"
  exit 2
fi

work="$(mktemp -d)"
pdb_pid=""
daemon_pids=()
cleanup() {
  for p in "${daemon_pids[@]:-}"; do
    [ -n "$p" ] && kill "$p" 2>/dev/null || true
  done
  if [ -n "$pdb_pid" ]; then
    kill "$pdb_pid" 2>/dev/null || true
  fi
  if [ "${KEEP_WORK:-0}" = 1 ]; then
    echo "Work directory kept at $work"
  else
    rm -rf "$work"
  fi
}
trap cleanup EXIT

# --- pathvector binary -------------------------------------------------------
if [ -z "${PATHVECTOR:-}" ]; then
  echo "==> Building pathvector"
  (cd "$repo_dir" && go build -o "$work/pathvector" .)
  PATHVECTOR="$work/pathvector"
fi
echo "==> Using pathvector binary $PATHVECTOR"

# --- PeeringDB test API --------------------------------------------------------
# Same fixture the BIRD matrix uses; generate-complex.yml sets
# peeringdb-url to http://127.0.0.1:5000/api and uses
# filter-never-via-route-servers, which makes pathvector query PeeringDB.
pdb_up() {
  python3 -c 'import urllib.request; urllib.request.urlopen("http://127.0.0.1:5000/api/net?asn=34553", timeout=1)' 2>/dev/null
}
if ! pdb_up; then
  echo "==> Starting PeeringDB test API"
  (cd "$repo_dir" && exec python3 tests/peeringdb/peeringdb-test-api.py) >"$work/peeringdb.log" 2>&1 &
  pdb_pid=$!
  for _ in $(seq 1 50); do
    pdb_up && break
    sleep 0.2
  done
  if ! pdb_up; then
    echo "PeeringDB test API did not start:" >&2
    cat "$work/peeringdb.log" >&2
    exit 1
  fi
fi
export PATHVECTOR_TEST=1

# --- test configs --------------------------------------------------------------
if [ -n "${CONFIGS:-}" ]; then
  read -r -a configs <<<"$CONFIGS"
else
  configs=("$repo_dir"/tests/generate-*.yml)
fi

# The configs reference ../tests/blocklist.txt and use relative cache paths.
# Run pathvector from $work/run with a tests symlink next to it so that the
# relative path resolves (same trick as the BIRD matrix).
mkdir -p "$work/run"
ln -s "$repo_dir/tests" "$work/tests"

failures=()
passes=0
xfails=0

XFAIL_FILE="${XFAIL_FILE:-$script_dir/xfail.txt}"
is_xfail() {
  [ -f "$XFAIL_FILE" ] || return 1
  sed -e 's/#.*//' "$XFAIL_FILE" | awk -v v="${1#frr-}" -v c="$2" '
    { sub(/^frr-/, "", $1) } $1 == v && $2 == c { found = 1 } END { exit !found }'
}

# Locate a binary under an FRR install root, preferring the usual spots.
find_bin() {
  local root="$1" name="$2"
  local c
  for c in \
    "$root/usr/sbin/$name" \
    "$root/usr/bin/$name" \
    "$root/usr/lib/frr/$name" \
    "$root/usr/lib/$name" \
    "$root/sbin/$name" \
    "$root/bin/$name"; do
    [ -x "$c" ] && { echo "$c"; return 0; }
  done
  find "$root" -type f -name "$name" -perm -u+x 2>/dev/null | head -1
}

# libfrr and friends live under the root's lib dirs; point the dynamic
# loader at them so DESTDIR binaries resolve their own libraries.
lib_path() {
  local root="$1" p=""
  local d
  for d in "$root/usr/lib" "$root/usr/lib/frr" "$root/lib" "$root/usr/local/lib"; do
    [ -d "$d" ] && p="$p:$d"
  done
  echo "${p#:}"
}

# Locate the directory holding FRR's loadable daemon modules (bgpd_rpki.so
# etc.) under an install root. RPKI is a module, not built into bgpd, so the
# daemon smoke test must load it with -M for the generated `rpki` node to be
# accepted. Prints nothing when the module is absent.
find_module() {
  local root="$1" name="$2" so d
  for so in \
    "$root/usr/lib/frr/modules/$name.so" \
    "$root/usr/local/lib/frr/modules/$name.so" \
    "$root/usr/lib/$name.so" \
    "$root/lib/$name.so"; do
    [ -f "$so" ] && { dirname "$so"; return 0; }
  done
  find "$root" -type f -name "$name.so" 2>/dev/null | head -1 | xargs -r dirname
}

# Kill any daemons left over from a previous test and clear the shared VTY
# sockets so each daemon_test starts from a clean slate. Tests run
# sequentially inside one container, so pkill by daemon name is safe.
reset_frr_state() {
  pkill -x zebra 2>/dev/null || true
  pkill -x bgpd 2>/dev/null || true
  pkill -x staticd 2>/dev/null || true
  sleep 0.5
  rm -f /var/run/frr/*.vty /var/run/frr/*.pid /var/run/frr/zserv.api \
        /var/run/frr/bgpd*.ser /etc/frr/*.pid 2>/dev/null || true
  rm -f /tmp/frr-*.pid 2>/dev/null || true
}

daemon_test() {
  local root="$1" vtysh="$2" name="$3" frr_conf="$4" test_yml="$5"
  local lp; lp="$(lib_path "$root")"
  local zebra bgpd
  zebra="$(find_bin "$root" zebra)"
  bgpd="$(find_bin "$root" bgpd)"
  [ -n "$zebra" ] && [ -n "$bgpd" ] || return 1

  # RPKI is a loadable bgpd module (bgpd_rpki.so), not compiled into the
  # daemon; load it so the generated `rpki` node is accepted. Locate the
  # module dir under this FRR root and pass it via -M/--moduledir.
  local moddir bgpd_mods=()
  moddir="$(find_module "$root" bgpd_rpki)"
  if [ -n "$moddir" ]; then
    bgpd_mods=(-M bgpd_rpki --moduledir "$moddir")
  fi

  # FRR's compiled-in dirs are shared across versions; make sure they exist
  # and vtysh has the marker file it requires. Daemons run as root (-u/-g
  # root) so no frr system user is needed inside the test container.
  mkdir -p /etc/frr /var/run/frr /var/lib/frr 2>/dev/null || true
  [ -f /etc/frr/vtysh.conf ] || : > /etc/frr/vtysh.conf
  # Only zebra+bgpd are exercised: the smoke test verifies that bgpd parses
  # and installs the generated neighbor/route-map config. staticd (which has
  # no -f flag and version-dependent options) is intentionally not started.
  printf 'zebra\nbgpd\n' > /etc/frr/daemons
  cp "$frr_conf" /etc/frr/frr.conf

  # Start from a clean slate: leftover daemons/sockets from a previous config
  # would make vtysh connect to dead endpoints.
  reset_frr_state

  local ok=1
  # Start the daemons; they read their (empty) per-daemon configs and open
  # their VTY sockets under the compiled-in runstate dir. $1 is the extra
  # daemonize flag ("-d" or empty for foreground).
  local d bin
  start_daemons() {
    local dmode="$1"
    daemon_pids=()
    for d in zebra bgpd; do
      case "$d" in
        zebra) bin="$zebra" ;;
        bgpd)  bin="$bgpd" ;;
      esac
      [ -n "$bin" ] || continue
      if [ "$d" = bgpd ]; then
        LD_LIBRARY_PATH="$lp" "$bin" $dmode -u root -g root -f "/etc/frr/$d.conf" \
          -i "/tmp/frr-$name-$d.pid" "${bgpd_mods[@]}" \
          >"$work/$name.$d.log" 2>&1 &
      else
        LD_LIBRARY_PATH="$lp" "$bin" $dmode -u root -g root -f "/etc/frr/$d.conf" \
          -i "/tmp/frr-$name-$d.pid" >"$work/$name.$d.log" 2>&1 &
      fi
      daemon_pids+=($!)
    done
  }

  # Wait until vtysh can actually reach bgpd. `show bgp summary` exits
  # non-zero ("failed to connect to any daemons") until bgpd's VTY socket is
  # up, and exits 0 once it answers — even before any BGP config is loaded.
  # Polling vtysh (rather than a hardcoded socket path) keeps this working
  # regardless of the FRR build's compiled-in runstate directory.
  wait_ready() {
    local _
    for _ in $(seq 1 80); do
      if LD_LIBRARY_PATH="$lp" "$vtysh" -c 'show bgp summary' >/dev/null 2>&1; then
        return 0
      fi
      sleep 0.25
    done
    return 1
  }

  # Try daemonized (-d) first so the smoke test also exercises the real
  # production path. Some older FRR releases reserve a huge virtual address
  # space and can no longer fork() during daemonize ("fork(): Cannot
  # allocate memory"); when that happens the daemons never come up, so fall
  # back to foreground mode, which is functionally identical for this test.
  start_daemons "-d"
  local ready=0
  if wait_ready; then
    ready=1
  else
    reset_frr_state
    start_daemons ""
    if wait_ready; then ready=1; fi
  fi
  if [ "$ready" = 1 ]; then
    if ! LD_LIBRARY_PATH="$lp" "$vtysh" -b >>"$work/$name.daemon.log" 2>&1; then
      ok=0
    fi
    # Give bgpd a moment to program the pushed config, then verify the BGP
    # process actually loaded neighbors from frr.conf. Neighbors will be in
    # Idle/Active (there is no real peer), which is fine — we only require
    # that bgpd parsed and installed the configuration.
    sleep 1
    local summary
    summary="$(LD_LIBRARY_PATH="$lp" "$vtysh" -c 'show bgp ipv4 unicast summary' 2>/dev/null || true)"
    # Neighbor rows always carry a state word (Idle/Active/OpenSent/...);
    # their presence proves bgpd installed the neighbor config from frr.conf.
    if ! grep -q "BGP router identifier" <<<"${summary:-}" \
       || ! grep -qE '\b(Idle|Active|OpenSent|OpenConfirm|Established|Connect)\b' <<<"${summary:-}"; then
      ok=0
    fi

    # Metrics smoke check: `pathvector metrics` on the FRR backend should
    # serve pathvector_frr_up 1 plus per-neighbor protocol series while the
    # daemons are up. Uses a private port; python3 does the HTTP fetch (same
    # approach as the PeeringDB stub probe above).
    if [ "$ok" = 1 ]; then
      local mport=19517 mpid mup=0
      LD_LIBRARY_PATH="$lp" "$PATHVECTOR" metrics -c "$test_yml" \
        -l "127.0.0.1:$mport" >"$work/$name.metrics.log" 2>&1 &
      mpid=$!
      for _ in $(seq 1 40); do
        if python3 - "$mport" <<'PY' 2>/dev/null
import sys, urllib.request
port = sys.argv[1]
body = urllib.request.urlopen(f"http://127.0.0.1:{port}/metrics", timeout=1).read()
sys.exit(0 if b"pathvector_frr_up 1" in body and b"pathvector_protocol_up{" in body else 1)
PY
        then mup=1; break; fi
        sleep 0.25
      done
      kill "$mpid" 2>/dev/null || true
      wait "$mpid" 2>/dev/null || true
      if [ "$mup" != 1 ]; then
        ok=0
        echo "    [daemon] metrics check failed for $name (see $work/$name.metrics.log):" >&2
        sed 's/^/      metrics| /' "$work/$name.metrics.log" 2>/dev/null | tail -8 >&2
      fi
    fi
  else
    ok=0
  fi

  if [ "$ok" != 1 ]; then
    # Surface diagnostics: vtysh -b output, the bgp summary we checked, and
    # the tail of each daemon log so failures are debuggable from CI output.
    echo "    [daemon] vtysh -b / summary diagnostics for $name:" >&2
    [ -f "$work/$name.daemon.log" ] && sed 's/^/      vtysh-b| /' "$work/$name.daemon.log" | tail -15 >&2
    echo "      summary| $(printf '%s' "${summary:-}" | head -3 | tr '\n' '|')" >&2
    for d in zebra bgpd; do
      [ -f "$work/$name.$d.log" ] && { echo "      $d.log|"; sed 's/^/        /' "$work/$name.$d.log" | tail -8 >&2; }
    done
  fi

  for p in "${daemon_pids[@]}"; do
    kill "$p" 2>/dev/null || true
  done
  daemon_pids=()
  # Stop any daemon that daemonized itself (double-fork) via its pidfile.
  for d in zebra bgpd; do
    [ -f "/tmp/frr-$name-$d.pid" ] && kill "$(cat "/tmp/frr-$name-$d.pid")" 2>/dev/null || true
  done
  return $((1 - ok))
}

for root in "$@"; do
  root="$(cd "$root" && pwd)"
  vtysh="$(find_bin "$root" vtysh)"
  if [ -z "$vtysh" ]; then
    echo "vtysh not found under $root" >&2
    exit 1
  fi
  # Version comes from the FRR_VERSION marker build-frr-versions.sh writes,
  # falling back to the parent directory name (the git tag).
  frr_version="$(cat "$root/FRR_VERSION" 2>/dev/null || basename "$(dirname "$root")")"
  echo "==> Testing FRR $frr_version ($root)"

  lp="$(lib_path "$root")"
  for config in "${configs[@]}"; do
    cname="$(basename "$config" .yml)"
    name="$frr_version-$cname"
    cache="$work/$name"
    # Point the config at the FRR backend, this vtysh and a private cache
    # directory. Drop any top-level setting of those keys first: duplicate
    # keys are a YAML error.
    {
      grep -Ev '^(backend|vtysh-binary|frr-directory|cache-directory):' "$config"
      echo
      echo "backend: frr"
      echo "vtysh-binary: $vtysh"
      echo "frr-directory: $work/$name-frr/"
      echo "cache-directory: $cache"
    } >"$work/$name.yml"

    step="generate"
    if (cd "$work/run" && LD_LIBRARY_PATH="$lp" "$PATHVECTOR" generate --dry-run --config "$work/$name.yml") >"$work/$name.log" 2>&1; then
      step="vtysh -C"
      if LD_LIBRARY_PATH="$lp" "$vtysh" -C -f "$cache/frr.conf" >>"$work/$name.log" 2>&1; then
        step="daemon"
        if [ "${DAEMON_TEST:-0}" != 1 ] || daemon_test "$root" "$vtysh" "$name" "$cache/frr.conf" "$work/$name.yml" >>"$work/$name.log" 2>&1; then
          if is_xfail "$frr_version" "$cname"; then
            echo "    XPASS $cname (listed in $(basename "$XFAIL_FILE"), remove the entry)"
            failures+=("FRR $frr_version: $cname (unexpected pass)")
          else
            echo "    PASS $cname"
            passes=$((passes + 1))
          fi
          continue
        fi
      fi
    fi
    if is_xfail "$frr_version" "$cname"; then
      echo "    XFAIL $cname ($step, known incompatibility)"
      xfails=$((xfails + 1))
      continue
    fi
    echo "    FAIL $cname ($step)"
    sed 's/^/      | /' "$work/$name.log"
    failures+=("FRR $frr_version: $cname ($step)")
  done
done

echo
echo "==> $passes passed, $xfails known failures, ${#failures[@]} failed"
for f in "${failures[@]}"; do
  echo "    $f"
done
[ ${#failures[@]} -eq 0 ]
