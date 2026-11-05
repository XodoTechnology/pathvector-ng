#!/usr/bin/env bash
# End-to-end smoke test: pathvector render/apply inside a real BIRD container,
# an eBGP peer, and the flask PeeringDB stub. Run from the repo root.
set -euo pipefail

COMPOSE="docker compose -p pv-e2e -f tests/e2e/docker-compose.yml"
pass=0; fail=0
check() { # check <name> <command...>
  local name=$1; shift
  if "$@" >/dev/null 2>&1; then echo "  PASS $name"; pass=$((pass+1));
  else echo "  FAIL $name"; fail=$((fail+1)); fi
}

echo "== building =="
go build -o tests/e2e/pathvector .
$COMPOSE build --quiet
$COMPOSE up -d --wait || $COMPOSE up -d

echo "== connectivity & BGP session =="
# wait for BIRD socket, then for the BGP session to establish (up to 60s)
for i in $(seq 1 30); do $COMPOSE exec -T peer birdc show status >/dev/null 2>&1 && break; sleep 2; done
est=0
for i in $(seq 1 30); do
  $COMPOSE exec -T peer birdc show protocols all to_router 2>/dev/null | grep -q "Established" && { est=1; break; }
  sleep 2
done
check "peer bird socket up"   $COMPOSE exec -T peer birdc show status
[ "$est" = 1 ] \
  && { echo "  PASS BGP session Established"; pass=$((pass+1)); } \
  || { echo "  FAIL BGP session Established"; $COMPOSE exec -T peer birdc show protocols all to_router || true; fail=$((fail+1)); }

echo "== route propagation =="
imp=0; exp=0
for i in $(seq 1 15); do
  $COMPOSE exec -T router birdc show route 185.121.170.0/24 all 2>/dev/null | grep -q "185.121.170.0/24" && imp=1
  $COMPOSE exec -T peer birdc show route 203.0.113.0/24 all 2>/dev/null | grep -q "203.0.113.0/24" && exp=1
  [ "$imp" = 1 ] && [ "$exp" = 1 ] && break
  sleep 2
done
[ "$imp" = 1 ] \
  && { echo "  PASS peer prefix imported on router"; pass=$((pass+1)); } \
  || { echo "  FAIL peer prefix imported on router"; $COMPOSE exec -T router birdc show route all | head -10; fail=$((fail+1)); }
[ "$exp" = 1 ] \
  && { echo "  PASS originated prefix exported to peer"; pass=$((pass+1)); } \
  || { echo "  FAIL originated prefix exported to peer"; $COMPOSE exec -T peer birdc show route all | head -10; fail=$((fail+1)); }

echo "== PeeringDB stub =="
$COMPOSE exec -T router curl -sf "http://pdb:5000/api/net?asn=65001" >/dev/null \
  && { echo "  PASS pdb stub reachable"; pass=$((pass+1)); } \
  || { echo "  FAIL pdb stub reachable"; fail=$((fail+1)); }

echo "== pathvector commands =="
# router bird socket readiness
for i in $(seq 1 20); do $COMPOSE exec -T router birdc show status >/dev/null 2>&1 && break; sleep 2; done
check "check"        $COMPOSE exec -T router pathvector check -c /etc/pathvector.yml --offline
check "status"       $COMPOSE exec -T router pathvector status -c /etc/pathvector.yml
check "status json"  sh -c "$COMPOSE exec -T router pathvector status -c /etc/pathvector.yml --json | grep -q '\"state\"'"
check "diff exit"    $COMPOSE exec -T router pathvector diff -c /etc/pathvector.yml --offline
$COMPOSE exec -T router sh -c "pathvector metrics -c /etc/pathvector.yml -l 127.0.0.1:9517 & sleep 1; curl -s http://127.0.0.1:9517/metrics" | grep -q "pathvector_bird_up 1" \
  && { echo "  PASS metrics endpoint"; pass=$((pass+1)); } \
  || { echo "  FAIL metrics endpoint"; fail=$((fail+1)); }

echo "== $pass passed, $fail failed =="
$COMPOSE down -v --remove-orphans >/dev/null 2>&1
[ "$fail" -eq 0 ]
