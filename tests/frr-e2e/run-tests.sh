#!/usr/bin/env bash
# End-to-end smoke test: pathvector render/apply inside a real FRR container
# (backend: frr), IPv4+IPv6 eBGP peers (one IRR-filtered), the route optimizer,
# and the flask PeeringDB stub. Same pathvector.yml peers/policy model as
# tests/e2e — only the backend differs. Peers themselves are BIRD, so birdc
# checks on the peer side are unchanged; router-side checks use vtysh.
# Run from the repo root.
set -euo pipefail

COMPOSE="docker compose -p pv-frr-e2e -f tests/frr-e2e/docker-compose.yml"
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

echo "== connectivity & BGP sessions =="
# wait for BIRD sockets on the peers, then for the BGP sessions to establish
# (up to 60s). Peer side uses birdc; router side uses vtysh.
for i in $(seq 1 30); do $COMPOSE exec -T peer birdc show status >/dev/null 2>&1 && break; sleep 2; done
est4=0; est6=0; estirr=0
for i in $(seq 1 30); do
  $COMPOSE exec -T peer birdc show protocols all to_router 2>/dev/null | grep -q "Established" && est4=1
  $COMPOSE exec -T peer birdc show protocols all to_router6 2>/dev/null | grep -q "Established" && est6=1
  $COMPOSE exec -T peer-irr birdc show protocols all to_router 2>/dev/null | grep -q "Established" && estirr=1
  [ "$est4" = 1 ] && [ "$est6" = 1 ] && [ "$estirr" = 1 ] && break
  sleep 2
done
check "peer bird socket up"   $COMPOSE exec -T peer birdc show status
[ "$est4" = 1 ] \
  && { echo "  PASS BGP IPv4 session Established"; pass=$((pass+1)); } \
  || { echo "  FAIL BGP IPv4 session Established"; $COMPOSE exec -T peer birdc show protocols all to_router || true; fail=$((fail+1)); }
[ "$est6" = 1 ] \
  && { echo "  PASS BGP IPv6 session Established"; pass=$((pass+1)); } \
  || { echo "  FAIL BGP IPv6 session Established"; $COMPOSE exec -T peer birdc show protocols all to_router6 || true; fail=$((fail+1)); }
[ "$estirr" = 1 ] \
  && { echo "  PASS IRR peer BGP session Established"; pass=$((pass+1)); } \
  || { echo "  FAIL IRR peer BGP session Established"; $COMPOSE exec -T peer-irr birdc show protocols all to_router || true; fail=$((fail+1)); }

# Router side: FRR must report the same sessions Established. FRR's summary
# does not print the word "Established" — established neighbors show a
# numeric PfxRcd while non-established ones show a state word
# (Idle/Active/OpenSent/Connect/NoNeg). Count neighbor lines lacking a
# state word. IPv4 unicast should show >=2 (Peer + IRRPeer); IPv6 unicast
# shows 1 (Peer's v6 neighbor; the v6 neighbor also appears in the v4
# summary as NoNeg and is correctly excluded).
rtr4=0; rtr6=0
for i in $(seq 1 20); do
  n4=$($COMPOSE exec -T router vtysh -c 'show bgp ipv4 unicast summary' 2>/dev/null \
       | grep -E '^[0-9a-fA-F]' | grep -cvE 'Idle|Active|OpenSent|OpenConfirm|Connect|NoNeg' || true)
  n6=$($COMPOSE exec -T router vtysh -c 'show bgp ipv6 unicast summary' 2>/dev/null \
       | grep -E '^[0-9a-fA-F]' | grep -cvE 'Idle|Active|OpenSent|OpenConfirm|Connect|NoNeg' || true)
  [ "${n4:-0}" -ge 2 ] && rtr4=1
  [ "${n6:-0}" -ge 1 ] && rtr6=1
  [ "$rtr4" = 1 ] && [ "$rtr6" = 1 ] && break
  sleep 2
done
[ "$rtr4" = 1 ] \
  && { echo "  PASS FRR ipv4 unicast sessions Established"; pass=$((pass+1)); } \
  || { echo "  FAIL FRR ipv4 unicast sessions Established"; $COMPOSE exec -T router vtysh -c 'show bgp ipv4 unicast summary' || true; fail=$((fail+1)); }
[ "$rtr6" = 1 ] \
  && { echo "  PASS FRR ipv6 unicast sessions Established"; pass=$((pass+1)); } \
  || { echo "  FAIL FRR ipv6 unicast sessions Established"; $COMPOSE exec -T router vtysh -c 'show bgp ipv6 unicast summary' || true; fail=$((fail+1)); }

echo "== route propagation =="
imp=0; exp=0; imp6=0; exp6=0
for i in $(seq 1 20); do
  $COMPOSE exec -T router vtysh -c 'show bgp ipv4 unicast' 2>/dev/null | grep -q "185.121.170.0/24" && imp=1
  $COMPOSE exec -T peer birdc show route 203.0.113.0/24 all 2>/dev/null | grep -q "203.0.113.0/24" && exp=1
  $COMPOSE exec -T router vtysh -c 'show bgp ipv6 unicast' 2>/dev/null | grep -q "2001:6464:1::/48" && imp6=1
  $COMPOSE exec -T peer birdc show route 2001:6464:2::/64 all 2>/dev/null | grep -q "2001:6464:2::/64" && exp6=1
  [ "$imp" = 1 ] && [ "$exp" = 1 ] && [ "$imp6" = 1 ] && [ "$exp6" = 1 ] && break
  sleep 2
done
[ "$imp" = 1 ] \
  && { echo "  PASS peer prefix imported on router"; pass=$((pass+1)); } \
  || { echo "  FAIL peer prefix imported on router"; $COMPOSE exec -T router vtysh -c 'show bgp ipv4 unicast' | head -15; fail=$((fail+1)); }
[ "$exp" = 1 ] \
  && { echo "  PASS originated prefix exported to peer"; pass=$((pass+1)); } \
  || { echo "  FAIL originated prefix exported to peer"; $COMPOSE exec -T peer birdc show route all | head -10; fail=$((fail+1)); }
[ "$imp6" = 1 ] \
  && { echo "  PASS peer IPv6 prefix imported on router"; pass=$((pass+1)); } \
  || { echo "  FAIL peer IPv6 prefix imported on router"; $COMPOSE exec -T router vtysh -c 'show bgp ipv6 unicast' | head -15; fail=$((fail+1)); }
[ "$exp6" = 1 ] \
  && { echo "  PASS originated IPv6 prefix exported to peer"; pass=$((pass+1)); } \
  || { echo "  FAIL originated IPv6 prefix exported to peer"; $COMPOSE exec -T peer birdc show route all | head -10; fail=$((fail+1)); }

echo "== IRR filtering =="
# The IRR peer announces 185.121.172.0/24 (in the as-set -> accepted) and
# 185.121.173.0/24 (not in the as-set -> rejected by filter-irr).
irr_ok=0; irr_bad=0
for i in $(seq 1 15); do
  $COMPOSE exec -T router vtysh -c 'show bgp ipv4 unicast' 2>/dev/null | grep -q "185.121.172.0/24" && irr_ok=1
  if $COMPOSE exec -T router vtysh -c 'show bgp ipv4 unicast' 2>/dev/null | grep -q "185.121.173.0/24"; then irr_bad=1; fi
  [ "$irr_ok" = 1 ] && break
  sleep 2
done
[ "$irr_ok" = 1 ] \
  && { echo "  PASS IRR-listed prefix imported"; pass=$((pass+1)); } \
  || { echo "  FAIL IRR-listed prefix imported"; $COMPOSE exec -T router vtysh -c 'show bgp ipv4 unicast' | grep 185.121 || true; fail=$((fail+1)); }
[ "$irr_bad" = 0 ] \
  && { echo "  PASS non-IRR prefix rejected"; pass=$((pass+1)); } \
  || { echo "  FAIL non-IRR prefix was imported"; fail=$((fail+1)); }

echo "== route optimizer =="
# The optimizer probes an unreachable target (100% loss) and lowers the peer's
# local-pref once. local-pref 120 -> 100 is rendered into the deployed
# route-map in /etc/frr/frr.conf (FRR equivalent of BIRD's bgp_local_pref).
$COMPOSE exec -d router pathvector optimizer -c /etc/pathvector.yml
opt=0
for i in $(seq 1 15); do
  $COMPOSE exec -T router sh -c 'grep -h "local-preference 100" /etc/frr/frr.conf 2>/dev/null' | grep -q "local-preference 100" && { opt=1; break; }
  sleep 2
done
[ "$opt" = 1 ] \
  && { echo "  PASS optimizer lowered local-pref"; pass=$((pass+1)); } \
  || { echo "  FAIL optimizer lowered local-pref"; $COMPOSE exec -T router sh -c 'grep -h "local-preference" /etc/frr/frr.conf' || true; fail=$((fail+1)); }
# Restore the configured local-pref (120) so the diff check below sees a clean
# config. The optimizer's deprefed map means it won't lower it again.
$COMPOSE exec -T router pathvector generate -c /etc/pathvector.yml >/dev/null 2>&1 || true

echo "== PeeringDB stub =="
$COMPOSE exec -T router curl -sf "http://pdb:5000/api/net?asn=65001" >/dev/null \
  && { echo "  PASS pdb stub reachable"; pass=$((pass+1)); } \
  || { echo "  FAIL pdb stub reachable"; fail=$((fail+1)); }

echo "== pathvector commands =="
# router vtysh readiness
for i in $(seq 1 20); do $COMPOSE exec -T router vtysh -c 'show bgp summary' >/dev/null 2>&1 && break; sleep 2; done
check "check"        $COMPOSE exec -T router pathvector check -c /etc/pathvector.yml --offline
check "status"       $COMPOSE exec -T router pathvector status -c /etc/pathvector.yml
check "status json"  sh -c "$COMPOSE exec -T router pathvector status -c /etc/pathvector.yml --json | grep -q '\"state\"'"
check "diff exit"    $COMPOSE exec -T router pathvector diff -c /etc/pathvector.yml --offline
$COMPOSE exec -T router sh -c "pathvector metrics -c /etc/pathvector.yml -l 127.0.0.1:9517 & sleep 1; curl -s http://127.0.0.1:9517/metrics" | grep -q "pathvector_frr_up 1" \
  && { echo "  PASS metrics endpoint"; pass=$((pass+1)); } \
  || { echo "  FAIL metrics endpoint"; fail=$((fail+1)); }

echo "== $pass passed, $fail failed =="
$COMPOSE down -v --remove-orphans >/dev/null 2>&1
[ "$fail" -eq 0 ]
