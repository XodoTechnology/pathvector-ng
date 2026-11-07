---
title: Backends
sidebar_position: 4
---

# Routing Daemon Backends

Pathvector NG is daemon-agnostic: the `backend` option selects which routing
daemon's native configuration format is rendered and which control channel is
driven. Everything above the daemon layer — peers, templates, PeeringDB,
IRR, communities, policies, the optimizer — is shared by every backend.

| Backend | Config output | Control channel | Status |
|---------|---------------|-----------------|--------|
| `bird` (default) | BIRD `*.conf` | `birdc` socket | Full feature set |
| `cairn` | `cairnd.toml` + `policy.conf` + `staticd.toml` | `cairnctrl` socket | Core feature set (see below) |
| `frr` | `frr.conf` | `vtysh` | Core feature set (see below) |

Each backend is exercised against released daemon versions by an automated
matrix (`tests/bird-matrix`, `tests/frr-matrix`, `tests/cairn-e2e`,
`tests/e2e`) — BIRD `v2.0.7`–`v3.4.0`, FRR `frr-9.1.3`–`frr-10.7.1`, Cairn
`0.1.1`. Regenerate the README's compatibility table with `make compat`.

Set it at the top level of `pathvector.yml`:

```yaml
asn: 64512
router-id: 192.0.2.1
backend: cairn
```

## Cairn backend

With `backend: cairn`, `pathvector generate` renders:

- **`cairnd.toml`** — AS, router ID, listen addresses, per-neighbor sessions
  (`[[neighbor]]`), local `[[network]]` origination, `[fib]`, `[metrics]`
  (native Prometheus endpoint) and `[[rpki.cache]]` when RPKI is enabled.
- **`policy.conf`** — shared prefix-lists/as-path lists plus one `filter`
  per peer per direction (`PV_<PEER>_IN` / `PV_<PEER>_OUT`).
- **`staticd.toml`** — only when `kernel.statics*` entries exist.

Peer options map onto Cairn's native neighbor fields where they exist
(`password`, `multihop`, `ttl-security`, `rr-client`, `rs-client`,
`allow-local-as`, `add-path-*`, `next-hop-self`, per-family listen
addresses, import limits → `max_prefixes`). Everything else is expressed in
the generated filters: prefix accept-lists, bogon/blocklist/prefix-length
filters, local-pref and community/as-path/prefix preferences, community
add/remove, prepends, private-AS stripping, `announce-*`, next-hop rewrites,
RPKI `roa_check()` and `enforce-peer-nexthop` (as a `bgp_next_hop` check).

Command mapping:

| Command | BIRD backend | Cairn backend |
|---------|--------------|---------------|
| `generate` / `check` | `bird -p` validation | `cairnd --check` validation |
| `diff` | cache vs `bird-directory` | cache vs `cairn-directory` |
| `status` | `birdc show protocols` | `cairnctrl status` + `neighbors` |
| `restart` | `birdc restart` | `cairnctrl reset <neighbor>` |
| `metrics` | pathvector `/metrics` from birdc | cairnd's native `[metrics]` endpoint |

Deploy is a hot reload: rendered files are copied into `cairn-directory`
and `cairnctrl reload` is issued — no daemon restart, no BGP session flap.

### Cairn-specific settings

| Option | Default | Description |
|--------|---------|-------------|
| `cairn-directory` | `/etc/cairn/` | Where `cairnd.toml`, `policy.conf`, `staticd.toml` are deployed |
| `cairnd-binary` | `/usr/sbin/cairnd` | Path to `cairnd` (used by `check`) |
| `cairnctrl-binary` | `/usr/bin/cairnctrl` | Path to `cairnctrl` (used by `status`/`restart`) |
| `cairn-socket` | `/run/cairn/cairnd.sock` | `cairnd` management socket |
| `staticd-binary` | `/usr/sbin/staticd` | Path to `staticd` (used by `check`) |

### Unsupported on Cairn (warned, not silently dropped)

Peer features with no Cairn equivalent are logged as warnings at generate
time: raw BIRD filter snippets (`session-global`, `pre/post-import-filter`,
`pre-export*`), `filter-transit-asns`, `filter-as-set`, `filter-aspa`,
`blackhole-*` communities, `filter-never-via-route-servers`, RFC 9234 roles,
BFD (requires holo), confederation, gateway/cost, extended-next-hop, L3VPN,
per-peer source addresses, `clear-path`, and export/receive limits.
Session-role conflicts Cairn enforces (`rr-client` on eBGP, `rs-client` /
`allow-local-as` on iBGP, `multihop` + `ttl-security` together) are resolved
by dropping the invalid option with a warning.

Cairn is early-release software; treat `backend: cairn` as experimental.

`tests/cairn-e2e/` exercises the backend end-to-end against a real `cairnd`:
IPv4+IPv6 BGP sessions and route propagation with a BIRD peer, plus
`check`/`status`/`diff`/`restart` and cairnd's native metrics endpoint
(14 assertions). Because Cairn is private-beta with no public package repo,
the suite takes the `cairn.deb` as a local build input — see its README.

## FRR backend

With `backend: frr`, `pathvector generate` renders a single integrated
**`frr.conf`** in Cisco-style syntax: a `router bgp` block with per-neighbor
sessions (split into `address-family ipv4 unicast` / `ipv6 unicast`),
`route-map`s for import/export policy, `ip prefix-list`s, `ip as-path
access-list`s, `bgp community-list`s and `bgp large-community-list`s, an
`rpki` node for RTR caches, plus `ip route`/`ip protocol bgp route-map`
statements for kernel export. Validation uses `vtysh -C -f` and deployment
copies the file into `frr-directory` and issues `vtysh -b` to push it to
running daemons.

Peer options map onto FRR neighbor/route-map primitives where they exist
(`password`, `multihop`, `ttl-security`, `add-path-*`, `next-hop-self`,
`maximum-prefix`, per-family limits, communities, prepends, `remove-private-as`,
`default-originate`, RPKI `match rpki`, kernel export route-maps). BIRD-only
constructs are expressed through route-map sequences or reported as warnings
— never silently dropped.

Command mapping:

| Command | BIRD backend | FRR backend |
|---------|--------------|-------------|
| `generate` / `check` | `bird -p` validation | `vtysh -C -f` validation |
| `diff` | cache vs `bird-directory` | cache `frr.conf` vs `frr-directory/frr.conf` |
| `status` | `birdc show protocols` | `vtysh -c "show bgp summary json"` |
| `restart` | `birdc restart` | `vtysh -c "clear bgp <neighbor>"` |
| `metrics` | pathvector `/metrics` from birdc | pathvector `/metrics` from `vtysh` |

Deploy is a config push: `frr.conf` + `pathvector.yml` are copied into
`frr-directory` and `vtysh -b` distributes them to the running daemons. A
plain file copy always happens; `vtysh -b` is best-effort (it only warns if
FRR is not running, since the file is still picked up on next start).

`pathvector metrics` polls `vtysh -c "show bgp summary json"` on each scrape
and exports the same `pathvector_protocol_up`, `pathvector_protocol_routes`
and `pathvector_bgp_session_info` series as the BIRD exporter, plus
`pathvector_frr_up` as the daemon health gauge (`pathvector_bird_up` on BIRD).
Because FRR reports one neighbor row per address family under a single peer
name, the FRR series carry an `afi` label (`ipv4-unicast`, `ipv6-unicast`, …)
to keep dual-stack sessions distinct — BIRD instead encodes the family in the
protocol `name`, and its `table` label is the BIRD routing-table name (a
different concept), so the label sets aren't identical across backends.

### FRR-specific settings

| Option | Default | Description |
|--------|---------|-------------|
| `frr-directory` | `/etc/frr/` | Where `frr.conf` and `pathvector.yml` are deployed |
| `vtysh-binary` | `/usr/bin/vtysh` | Path to `vtysh` (used by `check`/`status`/`restart`/deploy) |

### RPKI module note

FRR builds RPKI as a loadable module (`bgpd_rpki`), not into `bgpd` itself.
Pathvector emits the standard `rpki`/`rpki cache tcp` config, which `bgpd`
only accepts when started with `-M bgpd_rpki` (and the module directory
available). Most packaged FRR builds ship and auto-load this module; a
minimal source build may need the flag. `vtysh -C` validates the syntax
either way.

### Version compatibility

The generated config targets **FRR ≥ 10.1**. `tests/frr-matrix/` builds a
spread of releases from source and smoke-tests them (see its README for the
full results); the currently-tested set is `frr-9.1.3` through `frr-10.7.1`.

Two version-specific caveats:

- **RPKI (`rpki cache`).** Pathvector emits `rpki cache tcp <host> <port>`
  (the `tcp`/`ssh` keywords were added in FRR 10.1). FRR ≤ 10.0 uses
  `rpki cache <host> <port>` instead and rejects the newer form, so on those
  versions set `rpki-enable: false` (RPKI is optional) — everything else is
  unaffected.
- **FRR ≤ 8.x** predates libyang 2 and Python 3.13-era APIs; it is not
  covered by the matrix and is not expected to work without a legacy build
  environment.

### Unsupported on FRR (warned, not silently dropped)

Peer/global features with no clean FRR equivalent are logged as warnings at
generate time: raw BIRD filter snippets (`session-global`, `pre/post-import`,
`pre-export*`, `pre-import-accept`), `prefer-older-routes`, `transit-lock`,
`confederation`, `filter-aspa`/`filter-as-set`/`filter-transit-asns`,
`blackhole-*` communities, `filter-never-via-route-servers`, `remove-private-as`
on iBGP sessions, VRRP/BFD/MRT instances, `keepalived-config`, global
`source4`/`source6`, and `authorized-providers` (ASPA).
