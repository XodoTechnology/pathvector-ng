# FRR test matrix

Pathvector can render configuration for the FRR backend (`backend: frr` in
`pathvector.yml`, implemented in `pkg/frr`). FRR has no single "supported
version" floor like BIRD's `supportedMin`, so this harness builds a spread of
FRR releases from source and checks that the configuration Pathvector
generates is accepted by each of them — reporting which versions work and
which do not.

For every FRR version and every test config (`tests/generate-*.yml`) the
harness:

1. runs `pathvector generate --dry-run`, which renders `frr.conf` and
   validates it with `vtysh -C -f` (see `frr.Check` in `pkg/frr/frr.go`),
2. runs `vtysh -C -f` on the generated config again to report FRR's own
   error message, and
3. inside Docker (`DAEMON_TEST=1`, needs `--cap-add NET_ADMIN --cap-add
   SYS_ADMIN`): starts zebra/bgpd, pushes the integrated config with
   `vtysh -b`, and verifies bgpd reports configured neighbors via
   `show bgp summary`.

Only the daemons Pathvector needs are built (`bgpd`, `zebra`, `staticd`,
`vtysh` plus RPKI); every other FRR daemon is disabled at configure time to
keep the build small. `build-frr-versions.sh` only passes
`--enable/--disable` options the checked-out `configure` actually supports,
because these flags changed across FRR releases. The daemon smoke test
exercises zebra+bgpd only (staticd is not needed to verify BGP neighbor
config and its CLI options vary across releases); daemons run as root
(`-u root -g root`) so no `frr` system user is required in the container.

The test configs query PeeringDB, so the harness starts the local PeeringDB
test API from `tests/peeringdb/` (the same one `make test-setup` uses) and no
access to peeringdb.com is needed. The configs also list a blocklist URL;
failing to fetch it only logs a warning.

## Running it

With Docker (recommended), from the repository root:

```sh
make frr-matrix        # all versions in versions.txt
```

To test a subset, point `FRR_VERSIONS_FILE` at a file listing just those
versions (one per line, same format as `versions.txt`):

```sh
printf 'frr-10.3.4\nfrr-10.0.5 v2.1.148\n' > /tmp/frr-vers.txt
make frr-matrix FRR_VERSIONS_FILE=/tmp/frr-vers.txt
```

This builds one image per version (`pathvector-frr-matrix:<version>`) from
`tests/frr-matrix/Dockerfile` and runs it. The FRR build layer is cached, so
later runs only rebuild Pathvector. Extra `docker build` flags can be passed
with `DOCKER_BUILD_ARGS="..."`, and a different Dockerfile with
`FRR_MATRIX_DOCKERFILE=...`. A single version by hand:

```sh
docker build -f tests/frr-matrix/Dockerfile \
  --build-arg FRR_VERSION=frr-10.3.4 -t pathvector-frr-matrix:frr-10.3.4 .
docker run --rm --cap-add NET_ADMIN --cap-add SYS_ADMIN pathvector-frr-matrix:frr-10.3.4
```

Without Docker, you need the FRR build dependencies (see the `apt-get install`
line in `tests/frr-matrix/Dockerfile`), Go and `python3-flask`:

```sh
printf 'frr-10.3.4\n' > /tmp/frr-vers.txt
make frr-matrix-local FRR_VERSIONS_FILE=/tmp/frr-vers.txt
# or step by step:
tests/frr-matrix/build-frr-versions.sh frr-10.3.4        # -> tests/frr-matrix/frr-*/root/...
tests/frr-matrix/run-tests.sh tests/frr-matrix/frr-10.3.4/root
```

`run-tests.sh` takes these environment variables: `PATHVECTOR` (binary to
test, built with `go build` if unset), `CONFIGS` (configs to use),
`DAEMON_TEST=1` (daemon check, needs root, `NET_ADMIN`+`SYS_ADMIN`
capabilities and the compiled-in `/etc/frr` + `/var/run/frr` directories),
`KEEP_WORK=1` (keep the generated configs for debugging) and `XFAIL_FILE`.

## Continuous integration

`.github/workflows/frr-matrix.yml` runs the harness as a GitHub Actions
matrix with one job per line of `versions.txt` (each job runs `make frr-matrix
FRR_VERSIONS_FILE=/tmp/frr-version.txt` with a single-version file). It runs on
every branch push and pull
request, can be started by hand (workflow_dispatch), and is called by
`release.yml`: the goreleaser job `needs` the matrix, so a release tag is
only published when every FRR version passes.

## Choosing the FRR source

FRR is cloned from `https://github.com/FRRouting/frr.git` by default. Set
`FRR_REPO` (script) or `--build-arg FRR_REPO=...` (Docker) to use another
mirror. Tags are the stable `frr-X.Y.Z` releases; `-dev`/`-rc` tags are not
listed in `versions.txt`.

## Tested versions

`versions.txt` lists the default versions — the newest patch of each minor
line that builds in the trixie-based image:

| Version    | Line                                   | Notes                              |
|------------|----------------------------------------|------------------------------------|
| frr-9.1.3  | last 9.x release                       | needs libyang 2.x (see below)      |
| frr-10.0.5 | 10.0                                   | needs libyang 2.x; RPKI xfail      |
| frr-10.x   | current stable (10.1–10.7)             | builds against system libyang 3.x  |

FRR source builds are heavy, so the matrix covers release lines rather than
every patch. Add or remove tags in `versions.txt`; the CI workflow builds
its matrix from this file, so no workflow change is needed.

### libyang compatibility

FRR releases are tightly coupled to a specific libyang major version. The
image ships Debian trixie's `libyang-dev` (3.x), which only FRR ≥ 10.1
accepts — 10.1+ build against it directly. Older releases still call
libyang-2 APIs that were removed in libyang 3, so `versions.txt` gives them
a second field naming a libyang git ref to build from source
(`build-frr-versions.sh` builds it with CMake and points FRR's configure at
it via `PKG_CONFIG_PATH`/`CPPFLAGS`/`LDFLAGS`, then ships the resulting
`libyang.so` inside the per-version root). `frr-10.0.5` uses `v2.1.148`
(newer than its CI pin — it calls `lyd_trim_xpath`, added after `v2.1.128`).
`frr-9.1.3` uses `v2.1.80`, matching the libyang its own CI tested against.

### Not covered: frr-7.x / frr-8.x

`frr-7.5.1` and `frr-8.5.7` cannot build in this image for two independent
reasons: they need **libyang 1.x** (older still than the libyang-2 path
above), and they call the Python C-API `Py_SetStandardStreamEncoding`, which
was removed in Python 3.12+ (trixie ships 3.13). Testing them would require a
legacy, era-appropriate build environment (e.g. a bullseye-based image with
its own libyang and Python). They are intentionally absent from
`versions.txt`.

### RPKI syntax changed in FRR 10.1

Pathvector emits `rpki cache tcp <host> <port> preference N`. The `tcp`/`ssh`
keywords on `rpki cache` were introduced in FRR 10.1; FRR ≤ 10.0 instead
uses `rpki cache <host> <port> ...` with no keyword. There is no single
syntax both accept, and Pathvector renders a static config without knowing
the installed FRR version, so on FRR ≤ 10.0 the RPKI block fails `vtysh -C`.
Set `rpki-enable: false` on those versions (RPKI is optional) —
`generate-norpki.yml` exercises exactly that path and passes on every
version. `xfail.txt` records the RPKI-enabled configs as expected failures
for `frr-9.1.3` and `frr-10.0.5`.

### Daemonize quirk on frr-9.1.3

The `frr-9.1.3` daemons reserve ~96 GB of virtual address space at startup
(unrelated to Pathvector's config — `vtysh -C` validates it cleanly). That
is large enough that the `fork()` inside `-d` daemonization fails with
`ENOMEM`, so the smoke test's first daemonized attempt never comes up.
`run-tests.sh` detects this and falls back to running zebra/bgpd in
foreground mode (`&`-backgrounded, functionally identical for the test).
This is an FRR 9.1.3 build/runtime quirk, not a config incompatibility — the
generated `frr.conf` applies correctly once a daemon is running.

## Known failures

`xfail.txt` lists combinations of FRR version and config that are known not
to work, one per line as `<version> <config-name>` (config name without
`.yml`). They are reported as `XFAIL` and do not fail the run. If a listed
combination starts passing it is reported as `XPASS` and the run fails, so
the list cannot go stale.

Prefer fixing the renderer (`pkg/frr/frr.go`) — or emitting version-gated
syntax — over adding entries.

## Adding a version

1. Find the tag: `git ls-remote --tags https://github.com/FRRouting/frr.git 'frr-1*'`.
2. Try it: `make frr-matrix FRR_VERSIONS=frr-X.Y.Z`.
3. Add the tag to `versions.txt`.
4. If it fails because of a real incompatibility, fix `pkg/frr` or add an
   entry with the reason to `xfail.txt`.
