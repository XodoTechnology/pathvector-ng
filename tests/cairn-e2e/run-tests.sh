#!/usr/bin/env bash
# Cairn end-to-end smoke test: pathvector (backend: cairn) rendering into
# cairnd.toml/policy.conf/staticd.toml, deployed to a real cairnd, with a BIRD
# eBGP peer on a dual-stack network. Requires cairn.deb in tests/cairn-e2e/
# (private beta — see README.md). Run from the repo root.
set -euo pipefail

COMPOSE="docker compose -p pv-cairn-e2e -f tests/cairn-e2e/docker-compose.yml"
pass=0; fail=0
check() { # check <name> <command...>
  local name=$1; shift
  if "$@" >/dev/null 2>&1; then echo "  PASS $name"; pass=$((pass+1));
  else echo "  FAIL $name"; fail=$((fail+1)); fi
}

echo "== building =="
[ -f tests/cairn-e2e/cairn.deb ] || { echo "ERROR: tests/cairn-e2e/cairn.deb missing (Cairn is private-beta; see tests/cairn-e2e/README.md)"; exit 1; }
go build -o tests/e2e/pathvector .
$COMPOSE build --quiet
$COMPOSE up -d --wait || $COMPOSE up -d

echo "== connectivity & BGP sessions =="
# Peer (BIRD) sessions must Establish on both families; cairnd side confirmed
# via cairnctrl neighbors.
est4=0; est6=0
for i in $(seq 1 40); do
  $COMPOSE exec -T peer birdc show protocols all to_router 2>/dev/null | grep -q "Established" && est4=1
  $COMPOSE exec -T peer birdc show protocols all to_router6 2>/dev/null | grep -q "Established" && est6=1
  [ "$est4" = 1 ] && [ "$est6" = 1 ] && break
  sleep 2
done
check "peer bird socket up" $COMPOSE exec -T peer birdc show status
[ "$est4" = 1 ] \
  && { echo "  PASS BGP IPv4 session Established"; pass=$((pass+1)); } \
  || { echo "  FAIL BGP IPv4 session Established"; $COMPOSE exec -T peer birdc show protocols all to_router || true; fail=$((fail+1)); }
[ "$est6" = 1 ] \
  && { echo "  PASS BGP IPv6 session Established"; pass=$((pass+1)); } \
  || { echo "  FAIL BGP IPv6 session Established"; $COMPOSE exec -T peer birdc show protocols all to_router6 || true; fail=$((fail+1)); }
# cairnd must see the neighbors too.
$COMPOSE exec -T router cairnctrl neighbors --json 2>/dev/null | grep -qi "established" \
  && { echo "  PASS cairnd neighbors established"; pass=$((pass+1)); } \
  || { echo "  FAIL cairnd neighbors established"; $COMPOSE exec -T router cairnctrl neighbors || true; fail=$((fail+1)); }

echo "== route propagation =="
# Peer announces -> cairnd imports (check cairn RIB via cairnctrl routes).
# Cairn originates -> peer imports (check BIRD RIB on the peer).
imp=0; exp=0; imp6=0; exp6=0
for i in $(seq 1 20); do
  $COMPOSE exec -T router cairnctrl routes 185.121.170.0/24 2>/dev/null | grep -q "185.121.170.0/24" && imp=1
  $COMPOSE exec -T peer birdc show route 203.0.113.0/24 all 2>/dev/null | grep -q "203.0.113.0/24" && exp=1
  $COMPOSE exec -T router cairnctrl routes 2001:6464:1::/48 2>/dev/null | grep -q "2001:6464:1::/48" && imp6=1
  $COMPOSE exec -T peer birdc show route 2001:6464:2::/64 all 2>/dev/null | grep -q "2001:6464:2::/64" && exp6=1
  [ "$imp" = 1 ] && [ "$exp" = 1 ] && [ "$imp6" = 1 ] && [ "$exp6" = 1 ] && break
  sleep 2
done
[ "$imp" = 1 ] \
  && { echo "  PASS peer prefix imported on cairnd"; pass=$((pass+1)); } \
  || { echo "  FAIL peer prefix imported on cairnd"; $COMPOSE exec -T router cairnctrl routes 2>/dev/null | head -15; fail=$((fail+1)); }
[ "$exp" = 1 ] \
  && { echo "  PASS originated prefix exported to peer"; pass=$((pass+1)); } \
  || { echo "  FAIL originated prefix exported to peer"; $COMPOSE exec -T peer birdc show route all | head -10; fail=$((fail+1)); }
[ "$imp6" = 1 ] \
  && { echo "  PASS peer IPv6 prefix imported on cairnd"; pass=$((pass+1)); } \
  || { echo "  FAIL peer IPv6 prefix imported on cairnd"; fail=$((fail+1)); }
[ "$exp6" = 1 ] \
  && { echo "  PASS originated IPv6 prefix exported to peer"; pass=$((pass+1)); } \
  || { echo "  FAIL originated IPv6 prefix exported to peer"; $COMPOSE exec -T peer birdc show route all | head -10; fail=$((fail+1)); }

echo "== pathvector commands =="
for i in $(seq 1 20); do $COMPOSE exec -T router cairnctrl status >/dev/null 2>&1 && break; sleep 2; done
check "check"       $COMPOSE exec -T router pathvector check -c /etc/pathvector.yml
check "status"      $COMPOSE exec -T router pathvector status -c /etc/pathvector.yml
check "status json" sh -c "$COMPOSE exec -T router pathvector status -c /etc/pathvector.yml --json | grep -q '\"state\"'"
check "diff exit"   $COMPOSE exec -T router pathvector diff -c /etc/pathvector.yml
# restart maps to `cairnctrl reset` per neighbor — sessions flap then recover.
check "restart"     $COMPOSE exec -T router pathvector restart -c /etc/pathvector.yml --all

echo "== cairnd native metrics =="
# cairnd serves Prometheus metrics itself on the [metrics] listener
# (default 127.0.0.1:10065), independent of pathvector.
$COMPOSE exec -T router sh -c "curl -sf http://127.0.0.1:10065/metrics" | grep -qiE "cairn|bgp|neighbor" \
  && { echo "  PASS cairnd metrics endpoint"; pass=$((pass+1)); } \
  || { echo "  FAIL cairnd metrics endpoint"; $COMPOSE exec -T router sh -c "curl -s http://127.0.0.1:10065/metrics | head -20" || true; fail=$((fail+1)); }

echo "== $pass passed, $fail failed =="
$COMPOSE down -v --remove-orphans >/dev/null 2>&1
[ "$fail" -eq 0 ]
