# FRR end-to-end router image — pathvector (backend: frr) rendering into
# frr.conf, validated with `vtysh -C -f`, deployed to /etc/frr, then pushed
# into running zebra/bgpd via `vtysh -b`. Peers with the shared BIRD peers,
# so the same pathvector.yml as tests/e2e is used with only the backend
# (and backend-specific directory keys) changed.
#
# trixie ships FRR 10.3 — inside pathvector's tested range (9.1.3-10.7.1);
# bookworm's 8.4.4 is too old for the rendered config syntax.
FROM debian:trixie-slim
# bgpq4 is intentionally NOT installed from apt: the e2e suite uses the
# deterministic stub at tests/irr/bgpq4 (installed below) so filter-irr is
# exercised without a real IRR database.
RUN apt-get update && apt-get install -y --no-install-recommends \
    frr ca-certificates curl jq && rm -rf /var/lib/apt/lists/*
COPY tests/irr/bgpq4 /usr/local/bin/bgpq4
RUN chmod +x /usr/local/bin/bgpq4
WORKDIR /pathvector
COPY tests/e2e/pathvector /usr/local/sbin/pathvector
COPY tests/frr-e2e/router/pathvector.yml /etc/pathvector.yml
COPY tests/frr-e2e/router/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh && mkdir -p /etc/frr /var/run/frr /var/lib/frr /var/run/pathvector/cache /run/frr
ENTRYPOINT ["/entrypoint.sh"]
