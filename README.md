# reqdb

`reqdb` coordinates requirements and agent work.

- SQLite is the system of record.
- Requirement revisions and audit events are append-only.
- YAML is input only.
- One coordinator serves all agents.
- One CLI checks, traces, renders, and claims work.

The profile uses BRS, StRS, SyRS, and SRS. It aligns with
[ISO/IEC/IEEE 29148:2018](https://www.iso.org/standard/72089.html). It does not
claim conformance.

Version 1 has no automated backup or YAML export. This repository contains the
design. See [SPEC.md](SPEC.md) and the YAML input [examples](examples).
