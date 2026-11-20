# Changelog

All notable changes to Pathvector NG. This project uses `vM.m` versions cut automatically
from `main` once the e2e suite passes; PR titles feed the generated release notes.

## Unreleased

- **`pathvector audit`** — checks every originated prefix (top-level
  `prefixes` and `kernel.statics`) against the IRR route objects for the
  local ASN, reporting authorized, covered-by-parent-object, and missing
  prefixes. Exits non-zero when any originated prefix lacks an IRR route
  object — the most common cause of routes dropped by peers' IRR filters.

- **RPKI visibility** — `pathvector doctor` now reports ROA counts from
  BIRD's `rpki4`/`rpki6` tables, and the BIRD metrics exporter emits a
  `pathvector_rpki_roas` gauge so stale or empty RTR caches are visible.

- **Per-prefix `filter-irr-origin` on BIRD** — the option now renders
  per-prefix route-object checks: each as-set member's originated prefixes
  are queried from the IRR (`!g`/`!6` whois) and every received route must
  match an IRR-authorized (prefix, origin) pair, deduplicated into shared
  origin sets. Falls back to the as-set origin check on query failure or
  when more than 2048 prefixes are covered. FRR keeps the as-set-granularity
  check; Cairn remains unsupported.

- **`export-as-set` option** — announce routes only to peers whose ASN is a
  member of the given IRR as-set (resolved via `irr-servers`, cached like
  other IRR data). Non-member peers keep their import session but get export
  disabled; on IRR failure exports are left unchanged. Works on all backends.

- **PeeringDB `info_type`** — the `pdb` command shows the network's
  PeeringDB type, and `UpdateFromData` notes when a peer is a Route Server.

- **New inspect commands** — `pathvector irr as-set NAME` shows an IRR
  as-set's member ASNs and IPv4/IPv6 prefix sets, `pathvector irr aut-num
  ASN` prints the IRR aut-num object, `pathvector pdb ASN` shows PeeringDB
  network data (`--ixlans` for IX LAN attachments), and `pathvector doctor`
  health-checks the routing daemon, bgpq4, IRR/RTR servers, and PeeringDB
  (`--json` for machine-readable output).

- **`filter-irr-origin` peer option** — with `filter-irr`, additionally
  rejects routes whose origin ASN isn't in the peer's IRR as-set. Fetches
  as-set members like `auto-as-set-members` and renders the same origin
  check as `filter-as-set` on BIRD and FRR (reported as unsupported on
  Cairn).

- **IRR/RTR server fallback lists** — new `irr-servers` and `rtr-servers`
  options accept ordered server lists. IRR whois/bgpq4 queries try each
  server until one answers (a single server serves both address families of
  a peer's prefix query so results stay consistent, and the answering server
  is reused for `verify-irr-policy` as-set expansion). RTR redundancy is
  per-backend: BIRD gets one `protocol rpki` instance per server feeding the
  shared ROA tables, FRR gets `rpki cache tcp` entries with ascending
  preference, and Cairn gets one `[[rpki.cache]]` per server. The singular
  `irr-server`/`rtr-server` options keep working when the lists are unset.

- **Cairn backend (`backend: cairn`)** — Pathvector NG can now render and manage
  [Cairn](https://cairnrouting.org) (`cairnd`) natively: `cairnd.toml` +
  `policy.conf` + `staticd.toml` output, `cairnd --check` validation,
  `cairn-directory` deploy with hot `cairnctrl reload` (no session flap),
  `status`/`status --json`/`restart`/`diff` mapped onto `cairnctrl`, and Cairn's
  native Prometheus `[metrics]` endpoint. Peer/policy options are shared with the
  BIRD backend; unsupported features warn instead of silently dropping, and
  Cairn-enforced session-role conflicts (rr-client/rs-client/allowas-in,
  ttl-security+multihop) are resolved with warnings. `tests/cairn-e2e/` runs a
  live `cairnd` + BIRD-peer lab (IPv4/IPv6 sessions, route propagation,
  `check`/`status`/`diff`/`restart`, native metrics — 14 assertions); it takes
  the private-beta `cairn.deb` as a local input. Experimental — Cairn is
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
  generated configuration is valid across the 2.x→3.x boundary. The daemon
  test now also runs a real `pathvector generate` (no `--dry-run`) against
  the live daemon, so the deploy path — file move plus `configure` over the
  control socket, including rollback on rejection — is verified on every
  BIRD version, not just `bird -p` parse-validity.

- **Hardening pass** — whole-codebase review fixes: the generate lockfile is
  now removed on every exit path (a failed run no longer wedges later
  generates); library code no longer calls `log.Fatal`/`os.Exit` (errors
  propagate through the existing recover boundaries instead of killing the
  process mid-run); `pathvector version`/`config` dispatch the daemon
  version query per backend instead of always dialing the BIRD socket;
  `birdsh` exits on stdin EOF; blocklist fetches use a timeout'd client
  with status checks and guaranteed body cleanup; the IRR cache key
  includes the server; failed BIRD `configure`s are detected via the reply
  status code (the old `"8002"` text match was dead — reply codes are
  stripped before matching) so rollback actually triggers; live daemon
  configs are written atomically; BIRD status parsers and assorted index
  operations no longer panic on malformed daemon output.

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
