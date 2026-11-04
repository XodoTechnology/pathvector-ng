# Pathvector NG — Roadmap

Working list of warranted improvements, grouped by theme. Contributions welcome —
open an issue first for anything big.

## Config & apply safety

- [ ] `pathvector check` — parse + validate + dry-render without touching BIRD
      (usable as a CI lint for pathvector.yml).
- [ ] `pathvector diff` — render the new config and show a unified diff vs the
      live `bird-directory` before applying.
- [ ] Rollback on failed `configure` — keep the last-good directory, auto-restore
      and `configure` back if BIRD rejects the new config (`bird -p` pre-validates
      but a failed reload can still leave half-applied state).
- [ ] `configure soft` option for policy-only changes (avoid hard session resets).
- [ ] `pathvector drain <peer>` — tag graceful-shutdown / RFC8326 community,
      wait, then disable the session.
- [ ] Secrets indirection: `password-file:` / `env:` so MD5 strings never sit
      in the YAML.
- [ ] `version:`/`apiVersion:` field in pathvector.yml so future breaking
      schema changes can be detected and migrated explicitly.

## BGP / routing

- [ ] Multiple RTR servers / `rpki` protocol redundancy; expose RPKI validity
      in `status` output.
- [ ] ASPA via RTR (BIRD 3) when available — keep `authorized-providers` as a
      static fallback.
- [ ] BMP (BGP Monitoring Protocol) feed to an external collector.
- [ ] Flowspec/`blackhole` trigger helper — `pathvector blackhole <prefix>`.
- [ ] Per-neighbor options (today most knobs are per-peer across all neighbors).
- [ ] Graceful BGP restart knobs (`graceful restart`, LLGR, stale-route handling).
- [ ] TCP-AO authentication alongside MD5 (kernel + BIRD support permitting).
- [ ] `filtered-routes` introspection command (pairs with `keep-filtered`).
- [ ] Configurable IRR prefix-length bounds (`-R` currently fixed at 32/128).

## Observability

- [ ] `pathvector status --json` for machine consumption; structured JSON logs.
- [ ] Prometheus exporter: session state, per-peer route counts, optimizer
      latency/loss, last successful generate timestamp.
- [ ] Optional periodic MRT dump upload (MRT rendering already exists).

## Correctness / hygiene debt

- [ ] Consistent context.Context plumbing (PeeringDB/IRR queries, probe loop)
      for clean shutdowns.
- [ ] Optimizer: `modifyPref` subtracts the modifier on every run while alerts
      persist (ratchets local-pref down) — clamp to one depref until recovery,
      restore the original pref, persist state across restarts.
- [ ] Optimizer: split probe results by (peer, source, target) instead of
      merging multi-source peers together.
- [ ] Deterministic template rendering — sort map keys in renders for stable
      diffs.
- [ ] Dependency refresh pass (validator, cobra, golang.org/x/net, …) —
      dependabot is configured for weekly bumps.

## Tests / CI

- [x] BIRD test matrix (docker BIRD versions) in GitHub Actions on every
      push/PR and gating every release.
- [ ] Add BIRD 3.x to the matrix — channel/protocol semantics changed; NG
      should validate templates against it early.
- [ ] Golden-file tests for generated BIRD output per feature flag.
- [ ] `staticcheck` job alongside the existing `go vet` + `golangci-lint`/gosec.
- [ ] Signed releases: cosign/gpg checksum signatures + SBOM in goreleaser.
- [ ] Extend the PeeringDB test stub coverage (auto-import-limits,
      auto-as-set, NVRS paths).

## Packaging / ops

- [ ] Ship systemd units + tmpfiles for `/run/pathvector`.
- [ ] Debian package repo publish (`distrib/repo-update.py` exists — wire to CI).
- [ ] Container image for lab/CI use.
