---
title: Daemon Compatibility
sidebar_position: 5
---

# Daemon Compatibility

Pathvector is **one binary with one backend setting** — `backend:` in
`pathvector.yml` selects which routing daemon's native config is rendered and
which control channel is driven. The peer model, templates, PeeringDB, IRR,
communities, policy, and the optimizer are shared by every backend.

Every backend is verified against released daemon versions by automated test
matrices that run on every change to `dev`/`main`.

## Tested versions

| Backend | Daemon | Verified versions | Result |
|---------|--------|-------------------|--------|
| `bird` (default) | BIRD | v2.0.7 – v3.4.0 (15 releases) | all pass — one known XFAIL |
| `frr` | FRR | frr-9.1.3 – frr-10.7.1 (7 releases) | core pass — FRR ≤ 10.0 needs `rpki-enable: false` |
| `cairn` | Cairn | 0.1.1 | e2e pass (private beta) |

Regenerate this table from the matrix version files with `make compat`
(`tests/compat-report.sh`).

<details>
<summary>Every version exercised</summary>

**BIRD** — `v2.0.7` `v2.0.12` `v2.13.1` `v2.14` `v2.15.1` `v2.16.2` `v2.17.7`
`v2.18.4` `v2.19.3` `v2.20.0` `v3.0.5` `v3.1.9` `v3.2.4` `v3.3.3` `v3.4.0`

**FRR** — `frr-9.1.3` `frr-10.0.5` `frr-10.1.5` `frr-10.2.6` `frr-10.3.4`
`frr-10.5.5` `frr-10.7.1`

**Cairn** — `0.1.1`

</details>

## Checking your daemon

Run `pathvector compat` on the router to query the installed daemon's version
and compare it against the tested range:

```
$ pathvector compat
Backend:      bird
Daemon:       BIRD
Version:      3.4.0
Tested range: v2.0.7 – v3.4.0
Status:       supported
```

Status is `supported` inside the tested range, `newer-than-tested` above it
(probably fine, but not CI-covered — report issues), `older-than-tested` below
the minimum, or `unreachable`/`unknown` when the daemon can't be queried. Run
it on the router itself — `generate` is often run on a separate config host
where the daemon isn't installed.

## What each matrix entry checks

For every version in `versions.txt` the matrix:

1. **Generates** Pathvector's config for that backend.
2. **Validates** it with the daemon's own checker — `bird -p`,
   `vtysh -C`/`vtysh -b`, or `cairnd --check`.
3. **Runs a live daemon** on the generated config.
4. **Drives the control channel** — `status`, `restart`, `diff`, and the
   metrics endpoint.

On top of the per-version matrices, `tests/e2e` runs a full dual-stack BGP
lab (IPv4 + IPv6 sessions, route propagation both directions, IRR filtering,
optimizer probing, all CLI commands) against BIRD, and `tests/cairn-e2e` runs
the equivalent lab against Cairn.

## Support policy

- **BIRD** — minimum `v2.0.7` (`supportedMin` in `pkg/bird/bird.go`), enforced
  at runtime. No maximum: BIRD 3.x is fully supported.
- **FRR** — the generated config targets **≥ 10.1**. FRR ≤ 10.0 works with
  `rpki-enable: false` (the `rpki cache tcp` keywords were added in 10.1).
  FRR 7.x/8.x predate libyang 2 and can't build on the modern toolchain.
- **Cairn** — experimental. Cairn is early-release/private-beta; its `.deb`
  isn't publicly distributed, so `tests/cairn-e2e` needs a local package.

## Known limitations

These are documented, version-specific gaps — not silent failures:

| Version | Limitation |
|---------|------------|
| BIRD v2.0.7 | `generate-complex` fails: `transit-lock` `+` AS-path mask needs ≥ 2.0.8 (XFAIL) |
| FRR ≤ 10.0 | `rpki cache tcp` unsupported — set `rpki-enable: false` |
| FRR ≤ 8.x | Can't build on modern toolchain — unsupported |
| Cairn | Early-release; unsupported peer features are warned, not dropped |

## Adding a version

Append the release tag to the matching file — CI picks it up automatically:

- `tests/bird-matrix/versions.txt` — BIRD git tags (`vX.Y.Z`)
- `tests/frr-matrix/versions.txt` — FRR git tags (`frr-X.Y.Z`), with an
  optional second field for a source-built libyang ref on older releases
- `tests/cairn-e2e/` — Cairn releases (requires the private `.deb`)

If a new version exposes an incompatibility, add it to the matching
`xfail.txt` with a reason rather than silently skipping it — an unexpected
pass (XPASS) fails the build so stale entries get cleaned up.
