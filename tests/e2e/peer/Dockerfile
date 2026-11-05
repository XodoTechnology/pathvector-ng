FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends bird2 && rm -rf /var/lib/apt/lists/*
COPY tests/e2e/peer/bird.conf.tmpl /etc/bird/bird.conf.tmpl
COPY tests/e2e/peer/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]
