# FRR end-to-end smoke test

Same topology and assertions as `tests/e2e`, but the router runs the
**FRR backend** (`backend: frr`) instead of BIRD. The `pathvector.yml` is
the e2e config with only the backend key and backend-specific directory
settings changed — same peers, same policies, same IRR/optimizer/PeeringDB
exercises — proving the config model is backend-agnostic.

- **router** — Debian trixie + FRR 10.3 (`zebra`/`bgpd`/`vtysh`) + the
  locally-built `pathvector` binary + the deterministic `bgpq4` stub.
  On start it resolves the peers' addresses, runs `pathvector generate`
  (online — IRR queries run), which renders `frr.conf`, validates it with
  `vtysh -C -f`, and deploys it to `/etc/frr/`; the entrypoint then starts
  zebra/bgpd and pushes the config with `vtysh -b`.
- **peer**, **peer-irr**, **irr**, **pdb** — the *same* images and configs
  as `tests/e2e` (BIRD peers + stubs), reused via the compose file.

Notes:

- trixie is used because bookworm's FRR 8.4.4 predates pathvector's
  tested range (9.1.3–10.7.1).
- The router needs `SYS_ADMIN` in addition to `NET_ADMIN`: Debian's FRR
  `privs_init` requires `cap_sys_admin` in the bounding set.
- `rpki-enable: false` is set because FRR ≤10.0 needs the RPKI RTR module
  wired up; the suite exercises routing policy, not RPKI validation.
- Router-side assertions use `vtysh`; FRR's `show bgp ... summary` reports
  established sessions as numeric prefix counts rather than the word
  "Established", so the runner counts neighbor lines without a state word.

## Coverage

Identical to `tests/e2e` (19 checks): IPv4+IPv6 sessions on both sides of
the session, route propagation in both directions, IRR filtering
(accepted + rejected prefixes), route optimizer lowering local-pref,
PeeringDB stub, and `pathvector check|status|diff|metrics`
(`pathvector_frr_up 1`).

## Running

```sh
bash tests/frr-e2e/run-tests.sh
```
