# reqdb

`reqdb` tracks requirements and their implementation in code. It also
coordinates the tasks that implement or reconcile those requirements.

- One server owns a SQLite database.
- The same binary is the server and its CLI client.
- Requirements have immutable revisions and a current reconciliation state.
- Parent changes make all downstream requirements need reconciliation.
- Tasks have dependencies, leases, requirement links, and pull request links.
- Rendered documents are derived views for humans and LLMs.

The requirement hierarchy uses BRS, StRS, SyRS, and SRS. It aligns with
[ISO/IEC/IEEE 29148:2018](https://www.iso.org/standard/72089.html), but it does
not claim conformance.

This repository contains the version 1 design. See [SPEC.md](SPEC.md), the
[database schema](db/schema.sql), and the [input examples](examples).
