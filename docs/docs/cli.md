---
title: CLI Usage
sidebar_position: 6
---
# Usage
```
Pathvector NG is a declarative edge routing platform that automates route optimization and control plane configuration with secure and repeatable routing policy.

Usage:
  pathvector [command]

Available Commands:
  birdsh      Lightweight BIRD shell
  completion  Generate the autocompletion script for the specified shell
  config      Export configuration, optionally sanitized with logknife
  dump        Dump configuration
  generate    Generate router configuration
  help        Help about any command
  match       Find common IXPs for a given ASN
  optimizer   Start optimization daemon
  restart     Restart BIRD protocols
  status      Show protocol status
  version     Show version information
  yang        Print YANG model of the configuration

Flags:
  -c, --config string   YAML configuration file (default "/etc/pathvector.yml")
  -d, --dry-run         Don't modify configuration
  -h, --help            help for pathvector
      --lock string     Lock file (check disabled if empty)
  -n, --no-configure    Don't configure BIRD
  -t, --trace           Show trace log messages
  -v, --verbose         Show verbose log messages

Use "pathvector [command] --help" for more information about a command.
```
```

## metrics

`pathvector metrics` serves BIRD protocol state and route counts in Prometheus
exposition format. Default listen address is `127.0.0.1:9517` (override with
`--listen` or the `metrics-listen` config option). Point Prometheus at
`http://<host>:9517/metrics`.

Exposed series: `pathvector_bird_up`, `pathvector_protocol_up`,
`pathvector_protocol_routes{direction=imported|filtered|exported|preferred}`,
`pathvector_bgp_session_info`.
