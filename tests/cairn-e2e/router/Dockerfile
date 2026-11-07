# Cairn end-to-end router image.
#
# Cairn is early-release / private-beta: cairnrouting.org states source and
# docs publish with the first public release, so there is no public package
# repo. This image installs the cairn .deb that must be placed in this
# directory before building (see README.md). The harness itself — compose,
# config, entrypoint, assertions — is committed; the binary is a local input
# and cairn.deb is gitignored.
FROM debian:bookworm-slim
COPY tests/cairn-e2e/cairn.deb /tmp/cairn.deb
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl jq && \
    dpkg -i /tmp/cairn.deb && rm -rf /var/lib/apt/lists/* /tmp/cairn.deb
WORKDIR /pathvector
COPY tests/e2e/pathvector /usr/local/sbin/pathvector
COPY tests/cairn-e2e/router/pathvector.yml /etc/pathvector.yml
COPY tests/cairn-e2e/router/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh && mkdir -p /etc/cairn /var/run/pathvector/cache /run/cairn
ENTRYPOINT ["/entrypoint.sh"]
