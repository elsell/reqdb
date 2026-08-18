# Version 1 specification

This file is normative. `shall` marks a rule.

## Scope

`reqdb` shall manage requirements, traceability, tasks, evidence, and leases.
It shall have no web interface, LLM, plug-in system, or generic workflow
engine. Version 1 shall not infer requirement fulfillment, run tests, automate
backups, or export YAML.

## Authority

- SQLite shall be the system of record.
- One coordinator shall own the SQLite file on local storage.
- All clients shall use that coordinator.
- Clients shall not open the SQLite file through a network file system.
- YAML shall be an input format only.
- Requirement revisions and audit events shall be append-only.
- Repository policy shall grant approval. `reqdb` shall record it.

Generated documents, indexes, and client files shall not be authoritative.

## Objects

| Object | Purpose |
|---|---|
| Requirement | Gives one stable identity and its current revision. |
| Requirement revision | Stores one immutable requirement version. |
| Task | Defines one work unit. |
| Evidence | Links source or a test to one requirement revision. |
| Lease | Gives temporary task control to one agent. |
| Audit event | Records one state change. |

The database shall store parsed objects as canonical JSON. It shall not retain
YAML layout, comments, anchors, or aliases.

## Hierarchy

| Level | View | ID prefix | Parent level |
|---|---|---|---|
| Business | BRS | `BR-` | None |
| Stakeholder | StRS | `STR-` | Business |
| System | SyRS | `SYR-` | Stakeholder |
| Software | SRS | `SWR-` | System |

Each non-business requirement shall refine one or more requirements at the
immediate parent level. Each link shall name a parent revision. The refinement
graph shall be acyclic.

The profile shall be fixed. The renderer shall produce BRS, StRS, SyRS, and
SRS views. The profile aligns with ISO/IEC/IEEE 29148:2018. It does not claim
conformance.

## YAML input

- Input shall use UTF-8 and the YAML 1.2 Core Schema.
- One input file shall contain one object.
- Parsers shall reject duplicate keys.
- Parsers shall reject aliases, anchors, merge keys, and custom tags.
- Schemas shall reject unknown fields.
- IDs shall be stable and unique.

`check FILE` shall validate input without changing the database. `apply FILE`
shall validate and store input.

## Requirements

An approved requirement shall meet these rules:

- Its statement contains one lowercase `shall`.
- Its statement defines one obligation.
- Its verification criterion gives a pass or fail result.
- Each parent exists at the pinned revision.
- Each parent is approved.

`verification` shall describe a method and criterion. It shall not name a
script or store a result.

Mechanical checks cannot prove meaning. Review shall confirm that each
requirement is necessary, clear, feasible, and correct.

## Revisions

A requirement shall start at revision 1. Any stored content change shall
create the next revision. A revision shall not be updated or deleted.

`apply` shall use an expected current revision. Zero shall mean that the
requirement must not exist. A mismatch shall fail without a change.

One transaction shall:

1. Check the expected revision.
2. Insert the new revision.
3. Set it as current.
4. Append an audit event.

The content hash shall cover the complete parsed object. The tool shall encode
the object with RFC 8785 and hash it with SHA-256. Hashes shall use lowercase
hexadecimal.

Links shall retain their pinned revisions. A link that does not name the
current revision shall be stale.

## Graphs

| Relation | From | To | Effect |
|---|---|---|---|
| `refines` | Requirement revision | Requirement revision | Trace DAG |
| `depends_on` | Task | Task | Work DAG |
| `contributes_to` | Task | Requirement revision | Task scope |
| `impl` | Source | Requirement revision | Implementation evidence |
| `test` | Source | Requirement revision | Verification evidence |

Only `depends_on` shall control task order. Refinement shall not imply task
order.

Task completion shall not imply requirement fulfillment. Version 1 shall
report task state, trace coverage, and evidence separately.

Each task dependency shall exist. A task shall not depend on itself. The work
graph shall be acyclic.

`impact ID` shall walk reverse refinement links. It shall report tasks and
evidence that cite affected revisions. It shall report stale links. It shall
not change task state.

## Tasks

A task update shall use an expected current version. Zero shall mean that the
task must not exist. A mismatch shall fail without a change.

A task is ready when all these conditions are true:

- Its definition is valid.
- Its state is open.
- It has no current lease.
- Each dependency is complete for its current definition hash.
- Each referenced requirement is approved and current.

Ready tasks shall sort by descending priority, then by ID.

A task definition change shall increase its version. It shall reopen a
completed task. The audit log shall retain the prior completion.

The definition hash shall cover the complete parsed task object. It shall use
RFC 8785 and SHA-256.

## Leases

A claim shall use one database transaction. It shall increment the task fence
and create a unique lease. The lease shall contain the agent, claim time,
heartbeat time, and expiry time.

Heartbeat, release, and completion shall require the current lease ID and
fence. An expired lease has no authority. A new claim can replace it. No
background reaper is required.

Completion shall require an unexpired lease and a Git commit. It shall record
the task hash and commit. External quality gates shall test the change.

The default lease duration shall be 30 minutes. All times shall use UTC and
RFC 3339.

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

## Audit

Each successful state change shall append one audit event in the same
transaction. An event shall contain a sequence, time, actor, kind, entity, and
JSON data. An event shall not be updated or deleted.

Event kinds shall include requirement creation and revision, task creation and
change, lease claim and release, task completion, and evidence recording.

The audit log records database actions. It does not protect the database from
an administrator who can replace the file.

## CLI

| Command | Result |
|---|---|
| `reqdb serve --db FILE [--listen ADDR]` | Own the database and serve clients. |
| `reqdb check FILE` | Validate one YAML input file. |
| `reqdb apply FILE --if-current N` | Create or update one object. |
| `reqdb get ID[@REVISION]` | Show one object and its links. |
| `reqdb history REQUIREMENT` | List all requirement revisions. |
| `reqdb audit [ID]` | List audit events. |
| `reqdb render --out DIR` | Generate four specifications and a trace matrix. |
| `reqdb impact ID` | Show downstream impact and stale links. |
| `reqdb scan --commit SHA` | Record source evidence. |
| `reqdb ready` | List ready tasks. |
| `reqdb claim [TASK] --agent ID [--ttl DURATION]` | Claim one ready task. |
| `reqdb heartbeat LEASE --fence N [--ttl DURATION]` | Extend one lease. |
| `reqdb release LEASE --fence N` | Release one lease. |
| `reqdb complete LEASE --fence N --commit SHA` | Complete one task. |

Client commands shall use `--server URL` or `REQDB_SERVER`. The coordinator
shall create an absent database.

For `apply`, `--if-current` shall name the expected requirement revision or
task version. Zero shall mean that the object must not exist.

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
