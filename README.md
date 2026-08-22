# reqdb

`reqdb` tracks requirements and their implementation in code. It also
coordinates the tasks that implement or reconcile those requirements.

- One server owns a SQLite or PostgreSQL database.
- The same binary is the server and its CLI client.
- Requirements have immutable revisions and a current reconciliation state.
- Parent changes make all downstream requirements need reconciliation.
- Tasks have dependencies, leases, requirement links, and pull request links.
- Rendered documents are derived views for humans and LLMs.

The requirement hierarchy uses BRS, StRS, and optional SyRS levels. It aligns with
[ISO/IEC/IEEE 29148:2018](https://www.iso.org/standard/72089.html), but it does
not claim conformance.

This repository contains the version 1 design. See [SPEC.md](SPEC.md), the
[database schema](db/schema.sql), and the [input examples](examples).

## Use

Build and start the server with a new database. This release intentionally does
not migrate databases created by the version 1 schema.

```text
make build
export REQDB_PASSWORD='choose-a-password'
build/reqdb serve --db reqdb.sqlite --listen 127.0.0.1:8080
```

Open `http://127.0.0.1:8080` to see the live requirement and task trees. The UI
is in the binary. It needs no separate build or server.

Authenticate the CLI, create and select a project, then use the same commands as
before. Credentials are stored per server in `~/.config/reqdb/token.json` with
owner-only permissions. `REQDB_TOKEN` overrides the saved credential for CI.

```text
export REQDB_SERVER=http://127.0.0.1:8080
build/reqdb login --server "$REQDB_SERVER"
build/reqdb project create reqdb --name "reqdb"
build/reqdb project use reqdb
build/reqdb requirement create --from-file examples/requirements/BRS/BR-SESSION-001.yaml
build/reqdb requirement create --from-file examples/requirements/StRS/STR-SESSION-001.yaml
build/reqdb requirement create --from-file examples/requirements/SyRS/SYR-SESSION-001.yaml
build/reqdb requirement list
build/reqdb task create --from-file examples/tasks/T-0042.yaml
build/reqdb task workable
```

Use `--project ID` or `REQDB_PROJECT` to override the saved project and `--json`
for machine-readable output. Use `--actor ID` to label the actor in the audit
log; with a shared password this label is caller-supplied rather than a verified
identity. Use HTTPS whenever the server is accessed over a network.

SQLite remains the default database. To use PostgreSQL, select the backend and
provide its DSN through the environment:

```text
export REQDB_PASSWORD='choose-a-password'
export REQDB_DATABASE_URL='postgres://reqdb:password@db.example/reqdb?sslmode=require'
build/reqdb serve --database postgres --listen 0.0.0.0:8080
```

SQLite and PostgreSQL use the same store implementation behind database
adapters. An existing SQLite database is not automatically copied to
PostgreSQL.

Print the embedded build version with `build/reqdb version`. Released container
images show the same version in the web header and are published to
`ghcr.io/elsell/reqdb` with `latest`, semantic-version, and commit-SHA tags.

## Releases

Pushes to `main` update a release-please pull request from Conventional Commit
messages. Merging that pull request creates the GitHub release and publishes an
amd64 image. Configure the `RELEASE_PLEASE_TOKEN` Actions secret
with a GitHub token that can create repository contents and pull requests; the
workflow uses the built-in `GITHUB_TOKEN` only to publish the image to GHCR.
