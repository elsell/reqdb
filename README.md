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

## Use

Build and start the server:

```text
make build
build/reqdb serve --db reqdb.sqlite --listen 127.0.0.1:8080
```

Use the same binary as the client:

```text
export REQDB_SERVER=http://127.0.0.1:8080
build/reqdb requirement create --from-file examples/requirements/BRS/BR-SESSION-001.yaml
build/reqdb requirement create --from-file examples/requirements/StRS/STR-SESSION-001.yaml
build/reqdb requirement create --from-file examples/requirements/SyRS/SYR-SESSION-001.yaml
build/reqdb requirement create --from-file examples/requirements/SRS/SWR-SESSION-001.yaml
build/reqdb requirement list
build/reqdb task create --from-file examples/tasks/T-0042.yaml
build/reqdb task ready
```

Use `--json` for machine-readable output. Use `--actor ID` to identify the
actor in the audit log. The version 1 server allows all requests. Authorization
is behind an application interface so that a later adapter can enforce it.
