#!/bin/bash
set -e
# Wait until peer, peer-irr and the IRR whois stub are resolvable/reachable —
# docker assigns IPs dynamically. getent ahosts* returns one line per socket
# type (STREAM/DGRAM/RAW); take the first.
for i in $(seq 1 60); do
  PEER_V4_IP=$(getent ahostsv4 peer | awk 'NR==1{print $1}')
  PEER_IRR_IP=$(getent ahostsv4 peer-irr | awk 'NR==1{print $1}')
  [ -n "$PEER_V4_IP" ] && [ -n "$PEER_IRR_IP" ] && break
  sleep 1
done
[ -n "$PEER_V4_IP" ] || { echo "peer never resolved"; exit 1; }
[ -n "$PEER_IRR_IP" ] || { echo "peer-irr never resolved"; exit 1; }

# IPv6 neighbor address of the peer on the dual-stack e2e network.
for i in $(seq 1 30); do
  PEER_V6_IP=$(getent ahostsv6 peer | awk 'NR==1{print $1}')
  [ -n "$PEER_V6_IP" ] && break
  sleep 1
done
[ -n "$PEER_V6_IP" ] || { echo "peer v6 never resolved"; exit 1; }

# Wait for the IRR whois stub (verify-irr-policy + filter-irr query it).
for i in $(seq 1 30); do
  (exec 3<>/dev/tcp/irr/43) 2>/dev/null && { exec 3>&- 3<&-; break; }
  sleep 1
done

# This container's own IPv4 — the optimizer pings probe targets from it.
OPT_SRC_IP=$(hostname -I | awk '{print $1}')

sed -i \
  -e "s/PEER_V4_IP/$PEER_V4_IP/" \
  -e "s/PEER_V6_IP/$PEER_V6_IP/" \
  -e "s/PEER_IRR_IP/$PEER_IRR_IP/" \
  -e "s/OPT_SRC_IP/$OPT_SRC_IP/" \
  /etc/pathvector.yml

# Online mode: IRR queries (fake bgpq4 + whois stub) must run for the
# IRR-filtered peer. Renders frr.conf, validates with `vtysh -C -f`, and
# deploys it to /etc/frr/. vtysh -b inside Deploy is a no-op here (daemons
# not up yet); we push the config ourselves after starting them.
pathvector generate -c /etc/pathvector.yml

# Start zebra + bgpd. Debian's frr package keeps binaries under
# /usr/lib/frr; drop to the frr user (member of frrvty) like Debian''s init scripts; running as root fails
# the frrvty group check in privs_init.
mkdir -p /etc/frr /var/run/frr /var/lib/frr
[ -f /etc/frr/vtysh.conf ] || : > /etc/frr/vtysh.conf
printf 'zebra\nbgpd\n' > /etc/frr/daemons
/usr/lib/frr/zebra -d -u frr -g frr -f /etc/frr/zebra.conf -i /tmp/zebra.pid \
  > /var/log/zebra.log 2>&1 &
/usr/lib/frr/bgpd -d -u frr -g frr -f /etc/frr/bgpd.conf -i /tmp/bgpd.pid \
  > /var/log/bgpd.log 2>&1 &

# Wait until vtysh can reach bgpd, then push the integrated config.
for i in $(seq 1 80); do
  vtysh -c 'show bgp summary' >/dev/null 2>&1 && break
  sleep 0.25
done
vtysh -b
sleep 1

tail -f /dev/null
