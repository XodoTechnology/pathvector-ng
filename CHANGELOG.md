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
  early-release. FRR backend field reserved (`backend: frr`, planned).

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
