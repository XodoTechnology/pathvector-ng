#!/bin/bash
set -e
mkdir -p /run/bird
# getent ahosts* returns one line per socket type (STREAM/DGRAM/RAW); take the first.
for i in $(seq 1 60); do
  ROUTER_IP=$(getent ahostsv4 router | awk 'NR==1{print $1}')
  [ -n "$ROUTER_IP" ] && break
  sleep 1
done
[ -n "$ROUTER_IP" ] || { echo "router never resolved"; exit 1; }
PEER_IRR_IP=$(hostname -I | awk '{print $1}')
sed -e "s/PEER_IRR_IP_PLACEHOLDER/$PEER_IRR_IP/" -e "s/ROUTER_IP_PLACEHOLDER/$ROUTER_IP/" \
    /etc/bird/bird.conf.tmpl > /etc/bird/bird.conf
exec bird -f -c /etc/bird/bird.conf
