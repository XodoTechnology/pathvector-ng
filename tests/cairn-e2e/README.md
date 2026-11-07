# Cairn end-to-end smoke test

Exercises `backend: cairn` against a **real `cairnd`**: pathvector renders
`cairnd.toml` + `policy.conf` + `staticd.toml`, validates with
`cairnd --check`, deploys into `cairn-directory`, and a live `cairnd` +
`cairnctrl` exchange IPv4/IPv6 eBGP routes with a BIRD peer.

## Cairn is private-beta — you need the .deb

[cairnrouting.org](https://cairnrouting.org) states Cairn is "in early
testing with a small group of users" and that source code and docs publish
with the first public release. There is **no public package repo**, so the
harness can't download it. To run this suite, place the Cairn Debian
package in this directory:

```
tests/cairn-e2e/cairn.deb      # e.g. cairn_0.1.1+git7129.83a42fb0_amd64.deb
```

`cairn.deb` is **gitignored** — the binary is a local build input and is
never committed (it's early-release software the author hasn't published).
Without it, `run-tests.sh` exits with a clear error and `docker compose
build` fails at the `COPY`.

## Topology

Two containers on a private dual-stack bridge:

- **router** — Debian + the Cairn `.deb` (`cairnd`, `cairnctrl`, `staticd`)
  + the locally-built `pathvector`. On start it resolves `peer`, runs
  `pathvector generate` (backend `cairn`), and launches `cairnd`.
- **peer** — reuses the BIRD peer from `tests/e2e/peer` (dual-stack eBGP,
  announces `185.121.170.0/24`, `198.51.100.0/24`, `2001:6464:1::/48`).

## Coverage (14 assertions)

- IPv4 **and** IPv6 BGP sessions `Established` (BIRD peer side +
  `cairnctrl neighbors`).
- Route propagation both ways: peer prefixes in the cairnd RIB
  (`cairnctrl routes`), cairn-originated `203.0.113.0/24` +
  `2001:6464:2::/64` in the peer's BIRD RIB.
- `pathvector check` (`cairnd --check`), `status`, `status --json`,
  `diff`, `restart` (`cairnctrl reset`).
- `cairnd`'s **native** Prometheus `[metrics]` endpoint on
  `127.0.0.1:10065` (pathvector doesn't proxy it — see `backends.md`).

## Run

```
make cairn-e2e     # or: bash tests/cairn-e2e/run-tests.sh
```

Exit code is non-zero if any check fails.
