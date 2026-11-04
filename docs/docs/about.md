---
title: About
sidebar_position: 1
---

<img alt="Pathvector NG Logo" src="https://raw.githubusercontent.com/XodoTechnology/pathvector-ng/main/docs/static/img/black-border.svg" height="200" />

# Pathvector NG

**Pathvector NG (Next Generation)** is a community-maintained continuation of
[Pathvector](https://github.com/natesales/pathvector), the declarative edge
routing platform by Nate Sales that automates route optimization and control
plane configuration with secure and repeatable routing policy.

The upstream repository is no longer actively maintained. Pathvector NG picks
up where it left off: the outstanding upstream issues and unmerged pull
requests are resolved, the additions from the
[zappiehost/pathvector](https://github.com/zappiehost/pathvector) fork are
included, and maintenance continues on top.

Pathvector NG is a **drop-in replacement**: the binary is still `pathvector`,
the config file is still `pathvector.yml`, and existing configs, systemd units,
and `/etc` paths keep working unchanged.

## Provenance

Pathvector NG is built openly on top of other people's work. Credit where it
is due:

- **[natesales/pathvector](https://github.com/natesales/pathvector)** — the
  original project by Nate Sales and its
  [contributors](https://github.com/natesales/pathvector/graphs/contributors),
  MIT licensed. The full upstream history is preserved in this repository's
  git log.
- **Community contributors** — the pull requests and issue reports filed
  against upstream that were merged here, including work by l27001,
  Skyler Mäntysaari, SteveYi, Junxiao Shi, Zachary Seguin, Nick Hilliard,
  尤理衡 (Li-Heng Yu), Dennis de Houx, Hampton Moore, Alfie Foster, Nico,
  Joey Julian König, L8X, potvinp, CloudieLLC and others — see the commit
  history and the
  [contributors graph](https://github.com/XodoTechnology/pathvector-ng/graphs/contributors)
  for the complete list.
- **[zappiehost/pathvector](https://github.com/zappiehost/pathvector)** —
  Zappie Host's fork. Their changes are merged here: reordered import filters
  (local-pref/community policy applied before sanity filters, blackholing,
  and RPKI last), blackholing down to /32 and /128 via standard *and* large
  communities with a proper `RTD_BLACKHOLE` destination, host-route prefix
  bounds (`-R 32`/`-R 128` for IRR queries and out-of-bounds filtering),
  the `extended-next-hop` peer option, ASN-based local-pref taking
  precedence over the session default, community stripping before adding,
  sorted `status` output, and raised default import limits.

Bug reports for code inherited from upstream are welcome here — the goal is
one maintained tree, not fragmentation.

## Features

* Robust BGP route filtering with RPKI, IRR, and downstream AS cone, ASPA, never-via-RS and more
* Automatic configuration from PeeringDB
* Automatic route optimization by enriching the standard set of BGP attributes with latency and packet loss metrics
* Declarative configuration model: Want to track your changes? Just commit your file to version control.
* Data-plane agnostic: Pathvector works on servers, network switches, embedded devices, etc
* BFD and VRRP support
* Extensible Go plugin API
* Prometheus metrics — `pathvector metrics` exports protocol state, route counts, and BGP session
  info on `/metrics` for scraping

### Fixed upstream issues

At fork time the upstream tracker had a long tail of open bugs and feature
requests. Those are addressed here, including (upstream issue numbers):

* Template inheritance & list merging (#128), named communities (#213),
  per-peer `source4`/`source6` (#233, #255), `l3vpn` for VPNv4/VPNv6 (#252),
  multiple kernel tables (#118), overriding built-in protocols (#277),
  `prefix-prefs` (#196), `as-prefs4`/`as-prefs6` (#236), `gateway` (#197),
  `cost` (#190), `vip-interface` (#193), `restart` command (#219),
  YANG model generation (#22)
* `dont-announce`/`only-announce` split by address family (#282, #258),
  blackhole routes exempted from out-of-bounds (#208),
  `announce-originated` with origin-communities (#189), no default local-pref
  override on iBGP (#241), kernel protocols named kernel4/kernel6 (#207),
  kernel statics exported with SRD (#215), learned default routes preferred
  with `accept-default` (#242)
* Optimizer edits the live BIRD config instead of a cache copy (#235),
  `--trace` actually enables trace logging, `cli` alias restored (#224),
  `bird-socket` honored by all commands (#184), robust BIRD version
  parsing (#212)
* Fail-safe per peer on IRR errors (#202), cached IRR/PeeringDB fallback when
  sources are unreachable (#188), user `bgpq-args` sources respected (#192),
  `filter-irr`/`auto-as-set-members` validated against missing as-sets (#217),
  `verify-irr-policy` command (#165)
* Docker-based BIRD test matrix in CI and before releases (#216, #146),
  `advertise-hostname` placement regression test (#256), route-limit
  troubleshooting docs (#260), Team Cymru UTRS guide (#244),
  `peeringdb-cache` docs (#251), default import limits documented (#237),
  as-prefs precedence over community-prefs (#198)
* Plus the zappiehost improvements listed in [Provenance](#provenance)


## Build from source

Requires **Go ≥ 1.23** (the `go` directive in `go.mod`; any newer toolchain works):

```shell
git clone https://github.com/XodoTechnology/pathvector-ng.git
cd pathvector-ng
go build -o pathvector .
```

To stamp version info into the binary (recommended for production so `pathvector version` reports something useful):

```shell
go build -o pathvector \
  -ldflags "-X main.version=$(git describe --tags --always --dirty) \
            -X main.commit=$(git rev-parse --short HEAD) \
            -X main.date=$(date -u +%Y-%m-%d)" .
```

Builds without ldflags print a "development build" warning banner — cosmetic only.

## Runtime dependencies

Required:

* **BIRD ≥ 2.0.7** running with a control socket (default `/run/bird/bird.ctl`)

Optional, per feature:

* [bgpq4](https://github.com/bgp/bgpq4) — IRR prefix-list generation (`filter-irr`, `auto-as-set`); queries
  `irr.irrd3.net` by default, override with `irr-server`
* RTR server — RPKI filtering. Defaults to the project's public RTR at `rtr.irrd3.net:3233`;
  run your own with [gortr](https://github.com/cloudflare/gortr) and set `rtr-server`
* PeeringDB — defaults to the project's public cache at `https://pdb.irrd3.net/api`; override with
  `peeringdb-url` (e.g. `https://peeringdb.com/api` or a local cache)
* [keepalived](https://github.com/acassen/keepalived) — VRRP output

## Install

```shell
install -m0755 pathvector /usr/local/sbin/pathvector
install -m0640 /dev/null /etc/pathvector.yml       # then edit it
mkdir -p /etc/bird /var/run/pathvector/cache
```

Then run `pathvector generate -c /etc/pathvector.yml` on a schedule (e.g.
`0 */12 * * *` in cron) to render, validate, and apply the BIRD config.

## Migrating from upstream pathvector

1. Install `pathvector-ng` (the package `replaces:`/`conflicts:` upstream's `pathvector` package, and provides a
   `pathvector` binary in the same location).
2. Keep your existing `/etc/pathvector.yml` — every upstream option is still valid.
3. New knobs are opt-in; see `docs/docs/configuration.md` or `pathvector docs`.

## Branches and releases

- **`dev`** — development branch. All PRs target `dev`; every push builds a rolling `dev` prerelease.
- **`main`** — stable branch. `dev` is merged into `main` for release; tags `v<major>.<minor>.<patch>` on `main`
  run the BIRD version matrix and then GoReleaser, producing `pathvector-ng` `.deb`/`.rpm`/`.tar.gz` packages and
  vendor platform images (Arista, Juniper, MikroTik, Cisco IOx).

## Differences from upstream natesales/pathvector

* Every open issue/PR-class fix listed above — upstream is unmaintained
* New peer fields: `gateway`, `cost`, `l3vpn`, `source4`, `source6`, `vip-interface`,
  `prefix-prefs`, `as-prefs4`, `as-prefs6`, `extended-next-hop`, named communities
* `pathvector check` (CI-safe config lint), `pathvector restart`, `pathvector yang`, `pathvector verify-irr-policy`, `pathvector metrics` commands
* zappiehost improvements: policy-before-sanity import filter order, /32 and /128 blackholing with proper
  `RTD_BLACKHOLE`, standard+large blackhole communities, sorted `status` output
* Default import limits bumped to `1500000` (v4) / `1000000` (v6)
* Public infrastructure defaults point at project-hosted services instead of upstream's:
  `irr-server` → `irr.irrd3.net` (IRRd), `rtr-server` → `rtr.irrd3.net:3233` (RPKI RTR),
  `peeringdb-url` → `https://pdb.irrd3.net/api` (PeeringDB cache). All three can be
  overridden in `pathvector.yml`
* `go.mod` requires Go 1.23; library code returns errors instead of `log.Fatal` mid-pipeline

Everything else — peer options, filtering families, optimizer, plugins, VRRP/BFD/MRT — matches upstream. The
regenerated field reference is in `docs/docs/configuration.md`, or run `pathvector docs`.

## Development

```shell
go build ./...
go vet ./...
go test ./pkg/... ./cmd/...
```

## License

MIT — same as upstream. Original work © Nate Sales; modifications © their
respective contributors (see git history).
