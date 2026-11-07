#!/bin/bash
set -e
mkdir -p /run/bird
# getent ahosts* returns one line per socket type (STREAM/DGRAM/RAW); take the first.
for i in $(seq 1 60); do
  ROUTER_IP=$(getent ahostsv4 router | awk 'NR==1{print $1}')
  ROUTER_IP6=$(getent ahostsv6 router | awk 'NR==1{print $1}')
  [ -n "$ROUTER_IP" ] && [ -n "$ROUTER_IP6" ] && break
  sleep 1
done
[ -n "$ROUTER_IP" ] || { echo "router never resolved"; exit 1; }
[ -n "$ROUTER_IP6" ] || { echo "router v6 never resolved"; exit 1; }
PEER_IP=$(hostname -I | awk '{print $1}')
PEER_IP6=$(hostname -I | awk '{for(i=1;i<=NF;i++) if ($i ~ /:/) print $i; exit}')
[ -n "$PEER_IP6" ] || { echo "no local ipv6"; exit 1; }
sed -e "s/PEER_IP_PLACEHOLDER/$PEER_IP/" -e "s/ROUTER_IP_PLACEHOLDER/$ROUTER_IP/" \
    -e "s/PEER_IP6_PLACEHOLDER/$PEER_IP6/" -e "s/ROUTER_IP6_PLACEHOLDER/$ROUTER_IP6/" \
    /etc/bird/bird.conf.tmpl > /etc/bird/bird.conf
exec bird -f -c /etc/bird/bird.conf
