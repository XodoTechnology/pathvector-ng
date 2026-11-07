#!/bin/bash
set -e
mkdir -p /run/cairn
# Wait for the peer's addresses — docker assigns IPs dynamically.
# getent ahosts* returns one line per socket type (STREAM/DGRAM/RAW); take the first.
for i in $(seq 1 60); do
  PEER_V4_IP=$(getent ahostsv4 peer | awk 'NR==1{print $1}')
  PEER_V6_IP=$(getent ahostsv6 peer | awk 'NR==1{print $1}')
  [ -n "$PEER_V4_IP" ] && [ -n "$PEER_V6_IP" ] && break
  sleep 1
done
[ -n "$PEER_V4_IP" ] || { echo "peer v4 never resolved"; exit 1; }
[ -n "$PEER_V6_IP" ] || { echo "peer v6 never resolved"; exit 1; }

sed -i -e "s/PEER_V4_IP/$PEER_V4_IP/" -e "s/PEER_V6_IP/$PEER_V6_IP/" /etc/pathvector.yml

# Render + validate (cairnd --check) + deploy into /etc/cairn/. The reload
# attempt warns harmlessly since cairnd isn't up yet.
pathvector generate -c /etc/pathvector.yml

# Start cairnd in the foreground-managed background; it reads the deployed
# /etc/cairn/cairnd.toml + policy.conf at startup.
cairnd -c /etc/cairn/cairnd.toml &
CAIRND_PID=$!

# Wait for the management socket, then confirm cairnd is serving.
for i in $(seq 1 30); do
  [ -S /run/cairn/cairnd.sock ] && cairnctrl status >/dev/null 2>&1 && break
  sleep 1
done
[ -S /run/cairn/cairnd.sock ] || { echo "cairnd socket never appeared"; cat /var/log/syslog 2>/dev/null | tail; exit 1; }

tail -f /dev/null
