#!/bin/bash
set -e
# Wait until the peer container is resolvable — docker assigns IPs dynamically
for i in $(seq 1 60); do
  PEER_IP=$(getent hosts peer | awk '{print $1}')
  [ -n "$PEER_IP" ] && break
  sleep 1
done
[ -n "$PEER_IP" ] || { echo "peer never resolved"; exit 1; }
sed -i "s/PEER_IP/$PEER_IP/" /etc/pathvector.yml
pathvector generate -c /etc/pathvector.yml --no-configure --offline
bird -c /etc/bird/bird.conf -s /run/bird/bird.ctl
tail -f /dev/null
