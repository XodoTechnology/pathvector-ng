# End-to-end smoke test

Brings up a five-container topology on a private dual-stack bridge network:

- **router** — Debian + BIRD 2 + the locally-built `pathvector` binary + a
  deterministic `bgpq4` stub (`tests/irr/bgpq4`). On start it resolves the
  peers' IPv4/IPv6 addresses, renders `/etc/bird/` via
  `pathvector generate --no-configure` (online, so the IRR queries run), and
  starts BIRD.
- **peer** — a plain BIRD 2 instance holding IPv4 *and* IPv6 eBGP sessions to
  the router, announcing `185.121.170.0/24`, `198.51.100.0/24` and
  `2001:6464:1::/48` via real static routes.
- **peer-irr** — a second BIRD 2 eBGP peer (AS65003) announcing
  `185.121.172.0/24` (in the IRR as-set → accepted) and `185.121.173.0/24`
  (not in the as-set → rejected by `filter-irr`).
- **irr** — a minimal whois stub (`tests/irr/whois-stub.py`) answering the
  `verify-irr-policy` aut-num query for AS65003.
- **pdb** — the flask PeeringDB stub from `tests/peeringdb/`.

Container IPs are assigned dynamically — the lab works on hosts that already
have lots of addresses (this dev box included).

## Coverage

- IPv4 **and** IPv6 BGP sessions establish and exchange routes both ways.
- `filter-irr` + `verify-irr-policy`: the IRR-listed prefix is imported while
  the non-listed prefix is rejected. The `bgpq4` stub emits a fixed as-set so
  the test is deterministic; real bgpq4↔IRR interop is covered by bgpq4's own
  test suite, and `verify-irr-policy` uses a real TCP whois query.
- The route **optimizer**: with an unreachable probe target (100% loss) it
  lowers the peer's `bgp_local_pref` in the deployed per-peer BIRD config.
- `pathvector check`, `status`, `status --json`, `diff`, and the Prometheus
  `/metrics` endpoint.

## Run

```
make e2e        # or: bash tests/e2e/run-tests.sh
```

Assertions (17):
peer BIRD socket up; IPv4, IPv6 and IRR-peer sessions `Established`; IPv4 and
IPv6 prefixes imported on the router; router-originated `203.0.113.0/24` and
`2001:6464:2::/64` exported to the peer; IRR-listed prefix imported and
non-IRR prefix rejected; optimizer lowered local-pref; PeeringDB stub
reachable; `check`, `status`, `status --json`, `diff` exit 0; and the
Prometheus `/metrics` endpoint serves `pathvector_bird_up 1`.

Exit code is non-zero if any check fails.
