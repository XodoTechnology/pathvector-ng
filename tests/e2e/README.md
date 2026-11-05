# End-to-end smoke test

Brings up a three-container topology on a private bridge network:

- **router** — Debian + BIRD 2 + bgpq4 + the locally-built `pathvector` binary.
  On start it resolves `peer` by name, renders `/etc/bird/` via
  `pathvector generate --no-configure --offline`, and starts BIRD.
- **peer** — a plain BIRD 2 instance holding an eBGP session to the router and
  announcing `185.121.170.0/24` via a real (reachable) static route.
- **pdb** — the flask PeeringDB stub from `tests/peeringdb/`.

Container IPs are assigned dynamically — the lab works on hosts that already
have lots of addresses (this dev box included).

## Run

```
make e2e        # or: bash tests/e2e/run-tests.sh
```

Assertions (10):
socket up, BGP session `Established`, peer prefix imported on the router,
router-originated `203.0.113.0/24` exported to the peer, PeeringDB stub
reachable, `pathvector check`, `status`, `status --json`, `diff` exit 0,
and the Prometheus `/metrics` endpoint.

Exit code is non-zero if any check fails.
