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
| `frr` | — | — | Planned |

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
