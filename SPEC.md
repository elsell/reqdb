# Version 1 specification

This file is normative. `shall` marks a rule.

## Scope

`reqdb` shall manage requirement traceability and agent work claims. It shall
have no web interface, user service, LLM, plug-in system, or generic workflow
engine.

## Authority

- Git-tracked YAML is authoritative.
- SQLite contains mutable coordination state.
- Generated documents and indexes are not authoritative.
- Repository policy grants approval. `reqdb` does not grant approval.
- All agents shall use one state database.
- The state database shall not be in Git.
- Direct SQLite access shall occur on one host.

A multi-host deployment requires a service around the state database.

A project shall use this layout:

```text
requirements/BRS/
requirements/StRS/
requirements/SyRS/
requirements/SRS/
tasks/
```

Each file under `requirements` shall contain one requirement. Each file under
`tasks` shall contain one task.

## Objects

| Object | Storage | Purpose |
|---|---|---|
| Requirement | YAML | Defines one obligation. |
| Task | YAML | Defines one work unit. |
| Evidence | Source and SQLite | Links code or tests to a requirement. |
| Lease | SQLite | Gives temporary task control to one agent. |

One YAML file shall contain one object. Each object shall match its JSON
Schema.

## Hierarchy

| Level | Output | ID prefix | Parent level |
|---|---|---|---|
| Business | BRS | `BR-` | None |
| Stakeholder | StRS | `STR-` | Business |
| System | SyRS | `SYR-` | Stakeholder |
| Software | SRS | `SWR-` | System |

Each non-business requirement shall refine one or more requirements at the
immediate parent level. A refinement link shall include the parent revision.
The refinement graph shall be acyclic.

The version 1 profile shall be fixed. The renderer shall group requirements by
level. It shall generate BRS, StRS, SyRS, and SRS views.

## YAML

- Files shall use UTF-8 and YAML 1.2 Core Schema.
- Parsers shall reject duplicate keys.
- Parsers shall reject aliases, anchors, merge keys, and custom tags.
- Schemas shall reject unknown fields.
- IDs shall be stable and unique.

## Requirements

An approved requirement shall meet these rules:

- Its statement contains one lowercase `shall`.
- Its statement defines one obligation.
- Its verification criterion gives a pass or fail result.
- Each parent exists at the pinned revision.
- Each parent is approved.

`verification` describes a method and a criterion. It does not name a script
or store a result.

Mechanical checks cannot prove meaning. Review shall confirm that each
requirement is necessary, clear, feasible, and correct.

## Revisions

These fields are normative:

- `level`
- `type`
- `statement`
- `sources`
- `links`
- `verification`

A change to a normative field shall increase `revision`. The tool shall encode
normative fields with [RFC 8785](https://www.rfc-editor.org/rfc/rfc8785). It
shall hash the result with SHA-256. All hashes shall use lowercase hexadecimal.
A changed hash with the same revision shall fail validation.

Git retains prior content. The current file contains only the current revision.
A link to an older revision is stale.

## Graphs

| Relation | From | To | Effect |
|---|---|---|---|
| `refines` | Requirement | Requirement | Trace DAG |
| `depends_on` | Task | Task | Work DAG |
| `contributes_to` | Task | Requirement revision | Task scope |
| `impl` | Source | Requirement revision | Implementation evidence |
| `test` | Source | Requirement revision | Verification evidence |

Only `depends_on` controls task order. Requirement refinement shall not imply
task order.

Task completion shall not imply requirement fulfillment. Version 1 shall
report task state, trace coverage, and evidence separately.

Each task dependency shall exist. A task shall not depend on itself. The work
graph shall be acyclic.

`impact ID` shall walk reverse refinement links. It shall also report tasks and
evidence that cite the affected revisions. It shall report stale links. It
shall not change task state.

## Task readiness

A task is ready when all these conditions are true:

- Its definition is valid.
- Its state is open.
- It has no current lease.
- Each dependency is complete for its current definition hash.
- Each referenced requirement is approved and current.

Ready tasks sort by descending priority, then by ID.

A task definition change shall reopen a completed task. The event log shall
retain the prior completion.

The task definition hash shall cover the complete parsed task object. It shall
use RFC 8785 and SHA-256.

## Leases

A claim shall use one SQLite transaction. It shall increment the task fence
and create a unique lease. The lease shall contain the agent, claim time,
heartbeat time, and expiry time.

Heartbeat, release, and completion shall require the current lease ID and
fence. An expired lease has no authority. A new claim may replace it. No
background reaper is required.

Completion shall require an unexpired lease and a Git commit. It shall record
the task hash and commit. External quality gates shall test the change.

All times shall use UTC and RFC 3339.
The event log shall be append-only.

Event kinds are `task_reopened`, `lease_claimed`, `lease_released`,
`lease_reclaimed`, and `task_completed`.

## Evidence

Source comments can contain these markers:

```text
req:impl:SWR-SESSION-001@1
req:test:SWR-SESSION-001@1
```

`scan` shall record the kind, requirement revision, path, line, commit, and
content hash. The content hash shall use SHA-256 on the file bytes. Evidence
for an older revision is stale.

An approved software requirement shall have `impl` evidence. A software
requirement with test verification shall also have `test` evidence.

Evidence markers shall link existing tests. They shall not require one script
for each requirement.

## CLI

| Command | Result |
|---|---|
| `reqdb check [--base REF] [--coverage]` | Validate schemas, rules, and graphs. |
| `reqdb render --out DIR` | Generate four specifications and a trace matrix. |
| `reqdb show ID` | Show one object and its links. |
| `reqdb impact ID` | Show downstream impact and stale links. |
| `reqdb scan --commit SHA` | Record source evidence. |
| `reqdb ready` | List ready tasks. |
| `reqdb claim [TASK] --agent ID [--ttl DURATION]` | Claim one ready task. |
| `reqdb heartbeat LEASE --fence N [--ttl DURATION]` | Extend one lease. |
| `reqdb release LEASE --fence N` | Release one lease. |
| `reqdb complete LEASE --fence N --commit SHA` | Complete one task. |

`--root DIR` shall select the project root. It defaults to the current Git
root. The state database shall be `reqdb.sqlite3` in the Git common directory.
State commands shall create an absent database. The default lease duration
shall be 30 minutes.

Each command shall support `--json`. JSON output shall use one envelope:

```json
{"ok":true,"data":{}}
```

```json
{"ok":false,"error":{"code":"CODE","message":"Text."}}
```

JSON mode shall write only the envelope to standard output. Diagnostics shall
go to standard error. Exit code `0` means success, `1` means failure, and `2`
means invalid use. No ready task is a successful result with `data: null`.

`claim` shall return the task definition, requirement revisions, lease ID,
fence, expiry time, and definition hash.
