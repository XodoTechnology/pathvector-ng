#!/bin/bash
set -e
mkdir -p /run/bird
for i in $(seq 1 60); do
  ROUTER_IP=$(getent hosts router | awk '{print $1}')
  [ -n "$ROUTER_IP" ] && break
  sleep 1
done
[ -n "$ROUTER_IP" ] || { echo "router never resolved"; exit 1; }
PEER_IP=$(hostname -I | awk '{print $1}')
sed -e "s/PEER_IP_PLACEHOLDER/$PEER_IP/" -e "s/ROUTER_IP_PLACEHOLDER/$ROUTER_IP/" \
    /etc/bird/bird.conf.tmpl > /etc/bird/bird.conf
exec bird -f -c /etc/bird/bird.conf
