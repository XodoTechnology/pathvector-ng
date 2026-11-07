# FRR test matrix image: one FRR version built from source + pathvector.
#
# Build from the repository root (the build context must contain the source):
#   docker build -f tests/frr-matrix/Dockerfile \
#     --build-arg FRR_VERSION=frr-10.3.4 -t pathvector-frr-matrix:frr-10.3.4 .
#   docker run --rm pathvector-frr-matrix:frr-10.3.4
#
# or simply: make frr-matrix FRR_VERSIONS="frr-10.3.4 frr-8.5.7"
#
# See tests/frr-matrix/README.md for details.

# Debian trixie matches the GitHub Actions runner and ships every build
# dependency FRR needs (libyang 3, protobuf-c, rtrlib, ...). The Go stage
# only produces a static binary.
ARG DEBIAN_RELEASE=trixie
ARG GO_VERSION=1.24
# Any stable git tag of https://github.com/FRRouting/frr, e.g. frr-10.3.4
ARG FRR_VERSION=frr-10.3.4
ARG FRR_REPO=https://github.com/FRRouting/frr.git

# ---------------------------------------------------------------------------
# Stage 1: build FRR from a git tag (only bgpd/zebra/staticd/vtysh are kept)
# ---------------------------------------------------------------------------
FROM debian:${DEBIAN_RELEASE} AS frr
# Re-declare the global build args inside the stage so RUN can use them.
ARG FRR_VERSION
ARG FRR_REPO
# Optional libyang git tag for FRR releases that only build against an older
# libyang (e.g. frr-10.0.5 needs libyang 2.x; trixie ships libyang 3.x).
ARG LIBYANG_REF=
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      build-essential autoconf automake libtool pkg-config git ca-certificates \
      python3-dev cmake \
      libjson-c-dev libelf-dev libyang-dev libpcre2-dev libunwind-dev \
      protobuf-c-compiler libprotobuf-c-dev libreadline-dev librtr-dev \
      bison flex libsqlite3-dev libcap-dev libmnl-dev libncurses-dev \
 && rm -rf /var/lib/apt/lists/*

# Reuse the same build script used for local (non-Docker) builds so both
# paths configure FRR identically. Only the script is copied so the (slow)
# FRR build stays cached when pathvector sources change. LIBYANG_REF (when
# set) makes the script build that libyang from source for this FRR release.
COPY tests/frr-matrix/build-frr-versions.sh /build/
RUN FRR_REPO="${FRR_REPO}" /build/build-frr-versions.sh -o /opt/frr-build \
    "${FRR_VERSION}${LIBYANG_REF:+@${LIBYANG_REF}}" \
 && mv "/opt/frr-build/${FRR_VERSION}/root" /opt/frr-root

# ---------------------------------------------------------------------------
# Stage 2: build pathvector from the build context
# ---------------------------------------------------------------------------
FROM golang:${GO_VERSION} AS pathvector
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/pathvector .

# ---------------------------------------------------------------------------
# Stage 3: test runner
# ---------------------------------------------------------------------------
FROM debian:${DEBIAN_RELEASE}
ARG FRR_VERSION
# Runtime libraries of FRR (libfrr and its deps), python3-flask for the local
# PeeringDB test API (tests/peeringdb/) and ca-certificates for blocklist-urls.
# The -dev packages are installed because they are the reliable way to pull in
# the exact runtime library versions the FRR build linked against.
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      python3 python3-flask ca-certificates \
      libjson-c-dev libelf-dev libyang-dev libpcre2-dev libunwind-dev \
      libprotobuf-c-dev libreadline-dev librtr-dev libncurses-dev \
 && rm -rf /var/lib/apt/lists/*

# Materialize the FRR install tree under /opt/frr (binaries stay namespaced;
# their compiled-in config/socket dirs /etc/frr and /var/run/frr are shared
# and set up by run-tests.sh). LD_LIBRARY_PATH in run-tests.sh resolves the
# per-version libfrr.
COPY --from=frr /opt/frr-root /opt/frr
COPY --from=pathvector /out/pathvector /usr/local/bin/pathvector
# run-tests.sh locates the test configs and the PeeringDB fixtures relative
# to its own location, so keep the repository layout below /src
COPY tests/ /src/tests/

ENV FRR_VERSION=${FRR_VERSION} \
    PATHVECTOR=/usr/local/bin/pathvector \
    DAEMON_TEST=1
# vtysh refuses to run without its marker config; create the shared dirs.
RUN mkdir -p /etc/frr /var/run/frr /var/lib/frr \
 && [ -f /etc/frr/vtysh.conf ] || : > /etc/frr/vtysh.conf

WORKDIR /src
ENTRYPOINT ["/src/tests/frr-matrix/run-tests.sh"]
CMD ["/opt/frr"]
