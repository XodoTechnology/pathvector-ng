FROM debian:bookworm-slim
# bgpq4 is intentionally NOT installed from apt: the e2e suite uses the
# deterministic stub at tests/irr/bgpq4 (installed below) so filter-irr is
# exercised without a real IRR database. Real bgpq4<->IRR interop is covered
# by bgpq4's own test suite.
RUN apt-get update && apt-get install -y --no-install-recommends \
    bird2 ca-certificates curl jq && rm -rf /var/lib/apt/lists/*
COPY tests/irr/bgpq4 /usr/local/bin/bgpq4
RUN chmod +x /usr/local/bin/bgpq4
WORKDIR /pathvector
COPY tests/e2e/pathvector /usr/local/sbin/pathvector
COPY tests/e2e/router/pathvector.yml /etc/pathvector.yml
COPY tests/e2e/router/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh && mkdir -p /etc/bird /var/run/pathvector/cache /run/bird
ENTRYPOINT ["/entrypoint.sh"]
