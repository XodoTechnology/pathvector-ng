# Changelog

All notable changes to Pathvector NG. This project uses `vM.m` versions cut automatically
from `main` once the e2e suite passes; PR titles feed the generated release notes.

## Unreleased

- **Cairn backend (`backend: cairn`)** — Pathvector NG can now render and manage
  [Cairn](https://cairnrouting.org) (`cairnd`) natively: `cairnd.toml` +
  `policy.conf` + `staticd.toml` output, `cairnd --check` validation,
  `cairn-directory` deploy with hot `cairnctrl reload` (no session flap),
  `status`/`status --json`/`restart`/`diff` mapped onto `cairnctrl`, and Cairn's
  native Prometheus `[metrics]` endpoint. Peer/policy options are shared with the
  BIRD backend; unsupported features warn instead of silently dropping, and
  Cairn-enforced session-role conflicts (rr-client/rs-client/allowas-in,
  ttl-security+multihop) are resolved with warnings. Experimental — Cairn is
  early-release.

- **FRR backend (`backend: frr`)** — Pathvector NG now renders and manages
  [FRR](https://frrouting.org) natively: a single integrated `frr.conf`
  (`router bgp` + per-AF neighbors, route-maps, prefix-lists, as-path
  access-lists, community/large-community lists, `rpki` node, kernel-export
  route-maps), validated with `vtysh -C -f` before deploy, deployed to
  `frr-directory` and pushed with `vtysh -b`. `status`/`status --json` read
  `show bgp summary json`, `restart` maps to `clear bgp <neighbor>`, and
  `diff` compares the generated `frr.conf` against the live one. Import/export
  policy (allow-lists, AS-sets, bogons, RPKI, local-pref, communities,
  prepends, clear-path, blackhole, kernel export + SRD communities) is
  translated to route-map semantics; BIRD-only features warn rather than
  silently drop. `pathvector metrics` works on FRR too: it polls
  `vtysh -c "show bgp summary json"` and exports the same
  `pathvector_protocol_*`/`pathvector_bgp_session_info` series as BIRD (plus
  `pathvector_frr_up` as the daemon health gauge, with an `afi` label keeping
  dual-stack neighbors distinct).

- **FRR version matrix** — new `tests/frr-matrix/` harness builds FRR releases
  from source in Docker (only `bgpd`/`zebra`/`staticd`/`vtysh` + the
  `bgpd_rpki` module) and smoke-tests `generate` → `vtysh -C` → live daemon
  boot + `vtysh -b` + BGP neighbor verification across `frr-9.1.3` …
  `frr-10.7.1`. Older releases that need a different libyang build their own
  (the `TAG LIBYANG_REF` second column in `versions.txt`); `frr-7.x`/`8.x`
  are excluded (they predate libyang 2 and Python 3.13). `make frr-matrix`
  runs it locally; `.github/workflows/frr-matrix.yml` runs one job per
  version and gates stable releases (`release.yml`) on both matrices.
  Known incompatibilities live in `tests/frr-matrix/xfail.txt` — notably the
  `rpki cache tcp` syntax (FRR ≥ 10.1 only) on `frr-9.1.3`/`frr-10.0.5`.

- **BIRD matrix expansion** — `tests/bird-matrix/versions.txt` now covers
  `v2.0.7` … `v3.4.0`, spanning every BIRD 2.x and 3.x release line (adds
  v2.13.1, v2.16.2, v2.18.4, v2.20.0 and the v3.0.5–v3.4.0 lines). The
  generated configuration is valid across the 2.x→3.x boundary.

- **E2E coverage expansion** — `tests/e2e/` now runs a five-container
  dual-stack lab (added an IPv6 BGP session, an IRR-filtered peer, an IRR
  whois stub, and a deterministic `bgpq4` stub). It verifies IPv4+IPv6 route
  propagation, `filter-irr` accepting a listed prefix while rejecting an
  unlisted one, `verify-irr-policy`, the route optimizer lowering
  `bgp_local_pref`, and all `pathvector` commands + metrics — 17 assertions.

## v0.1 — first Pathvector NG release

Based on `natesales/pathvector` (upstream, unmaintained) + `zappiehost/pathvector` + fixes
for the open upstream issues/PRs. Highlights:

- 43 upstream-issue fixes: template inheritance & list merging, named communities,
  `gateway`, `cost`, `l3vpn`, `source4`/`source6`, `vip-interface`, `prefix-prefs`,
  `as-prefs4`/`as-prefs6`, multi-kernel-tables, `restart`/`yang`/`verify-irr-policy`,
  per-AF announce controls, `enforce-peer-nexthop`, optimizer live-config, IRR fail-safe
  + disk-cache fallback, `--trace`, `bird-socket` honored, BIRD version matrix CI
- zappiehost: policy-before-sanity import filter order, /32 + /128 blackholing via
  standard & large communities (`RTD_BLACKHOLE`), `-R 32`/`-R 128` IRR bounds,
  `extended-next-hop`, ASN-pref precedence, sorted `status`
- New commands: `check` (config lint), `diff` (rendered-vs-live diff), `status --json`,
  `metrics` (Prometheus exporter on `/metrics`)
- Safety: rollback to last-good config on failed `configure`, `log.Fatal` → errors,
  panic recovery in the parallel peer loop
- `password-file`/`password-env` secret indirection; `irr-prefix-length4/6` bounds
- Defaults: `irr.irrd3.net`, `rtr.irrd3.net:3233`, `https://pdb.irrd3.net/api`
- Optimizer: restores local-pref on recovery, probe failures no longer kill the daemon
- Packaging: `pathvector-ng` deb/rpm (`replaces`/`conflicts` upstream `pathvector`),
  systemd units incl. `pathvector-generate.timer`; container images; release PDF
- CI: e2e docker smoke test (BGP session + route propagation), bird-matrix on all
  supported BIRD versions, auto-release on `main`, `dev-snapshot` prereleases on `dev`,
  dependabot targeting `dev`

Historical notes: the zappiehost additions were verified merged before NG; commits in
this history credit original authors (natesales contributors + Zappie Host).
