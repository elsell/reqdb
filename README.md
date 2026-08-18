# reqdb

`reqdb` coordinates requirements and agent work.

- Git tracks YAML requirements, tasks, and project configuration.
- SQLite tracks leases, results, evidence, and events.
- One CLI checks, traces, renders, and claims work.

The profile uses BRS, StRS, SyRS, and SRS. It aligns with
[ISO/IEC/IEEE 29148:2018](https://www.iso.org/standard/72089.html). It does not
claim conformance.

This repository contains the version 1 design. See [SPEC.md](SPEC.md),
[reqdb.yaml](reqdb.yaml), and [examples](examples).
