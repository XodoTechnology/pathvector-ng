FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    bird2 bgpq4 ca-certificates curl jq && rm -rf /var/lib/apt/lists/*
WORKDIR /pathvector
COPY tests/e2e/pathvector /usr/local/sbin/pathvector
COPY tests/e2e/router/pathvector.yml /etc/pathvector.yml
COPY tests/e2e/router/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh && mkdir -p /etc/bird /var/run/pathvector/cache /run/bird
ENTRYPOINT ["/entrypoint.sh"]
