# Version 1 specification

This file is normative. `shall` marks a rule.

## Purpose

`reqdb` shall track requirements and their implementation in code. It shall
also coordinate the tasks that implement or reconcile those requirements.

Version 1 shall manage requirements, traceability, reconciliation, tasks,
leases, pull request links, audit events, and rendered views. It shall not run
tests, manage development constraints, or manage agent workflows.

## Authority

- SQLite shall be the system of record.
- One server shall own the SQLite file on local storage.
- All CLI commands shall use the server.
- YAML shall be an input format only.
- Requirement revisions and audit events shall be append-only.
- Rendered files shall not be authoritative.

## Requirement hierarchy

| Level | View | ID prefix | Meaning | Parent |
|---|---|---|---|---|
| Business | BRS | `BR-` | A result that the organization wants. | None |
| Stakeholder | StRS | `STR-` | An outcome that a stakeholder needs. | Business |
| System | SyRS | `SYR-` | Observable system behavior or a measurable system property. | Stakeholder |
| Software | SRS | `SWR-` | Software behavior that satisfies a system requirement. | System |

Each non-business requirement shall refine one or more requirements at the
immediate parent level. Each link shall name a parent revision. The refinement
graph shall be acyclic.

An input requirement shall have one objectively verifiable obligation. Its
statement shall contain one lowercase `shall`.

## Revisions

A requirement shall have a stable ID and immutable revisions. A content change
shall create the next revision. The database shall identify one current
revision.

`apply` shall use the expected current revision. A mismatch shall cause no
change.

## Reconciliation

Reconciliation describes the relation between a requirement revision and the
code.

| State | Meaning |
|---|---|
| `unimplemented` | No accepted implementation exists. |
| `in_progress` | An active task implements or reconciles the requirement. |
| `implemented` | A confirmation states that the code matches the requirement revision. |
| `needs_reconciliation` | The requirement or one of its ancestors changed after confirmation. |

A confirmation shall name the requirement revision, Git commit, actor, time,
and result. The result shall state that code changed or that existing code was
confirmed.

Task completion shall not confirm implementation by itself. A confirmation
may refer to the task and pull request that produced the result.

When a requirement gets a new revision, the server shall:

1. Set the requirement to `unimplemented`.
2. Find all transitive descendants.
3. Set each descendant to `needs_reconciliation`.
4. Record the changed ancestor as an unresolved cause for each affected
   descendant.

An active implementation or reconciliation task may set an unimplemented or
affected requirement to `in_progress`. A confirmation shall resolve the known
causes for that requirement revision and set it to `implemented`.

## Tasks and leases

A task shall have a title, description, priority, state, requirement links,
and task dependencies. A requirement link shall have the purpose `implement`
or `reconcile`.

A task is ready when it is open, has no active lease, and all its dependencies
are complete. Ready tasks shall sort by descending priority and then by ID.

A claim shall create one lease in one transaction. A lease shall identify the
task, agent, fence, claim time, and expiry time. Claim, heartbeat, release, and
completion operations shall use the current lease and fence.

A completed task shall record its Git commit. A task can link to zero or more
pull requests. A pull request can link to more than one task.

## Audit

Each successful state change shall append an audit event in the same
transaction. An audit event shall identify the time, actor, request IDs, kind,
entity, and event data. It shall not be updated. The server shall delete audit
events that exceed its configured retention period.

## Input

- Input shall use UTF-8 and the YAML 1.2 Core Schema.
- One input file shall contain one object.
- Parsers shall reject duplicate keys and unknown fields.
- Parsers shall reject aliases, anchors, merge keys, and custom tags.
- A resource `check` action shall validate input without a database change.
- A resource `create` or `update` action shall validate and store input.

## Server, CLI, and rendering

The same `reqdb` binary shall run the server and act as a CLI client. Client
commands shall use `--server URL` or `REQDB_SERVER`.

Resource commands shall use this form:

```text
reqdb RESOURCE ACTION [ID] [OPTIONS]
```

Resource names shall be singular. IDs shall be positional arguments. Options
shall supply attributes and operation data. Commands that use the same action
shall use the same action name.

The core resource commands are:

| Command | Result |
|---|---|
| `requirement list` | List requirements. |
| `requirement get ID[@REVISION]` | Show one requirement revision. |
| `requirement check --from-file FILE` | Validate one requirement file. |
| `requirement create --from-file FILE` | Create a requirement. |
| `requirement update ID --from-file FILE` | Create a requirement revision. |
| `requirement confirm ID[@REVISION] --commit SHA` | Confirm that code matches a revision. |
| `requirement render ID` | Render one requirement and its context. |
| `task list` | List tasks. |
| `task get ID` | Show one task. |
| `task create [OPTIONS]` | Create a task. |
| `task ready` | List ready tasks. |
| `task lease ID --agent ID` | Lease one task. |
| `task complete ID --lease ID --fence N --commit SHA` | Complete one task. |
| `task link-pr ID --url URL` | Link a task to a pull request. |
| `lease heartbeat ID --fence N` | Extend one lease. |
| `lease release ID --fence N` | Release one lease. |

Server-wide and graph-wide operations shall remain top-level commands:

| Command | Result |
|---|---|
| `serve` | Own the SQLite database and serve clients. |
| `trace [REQUIREMENT]` | Show the requirement hierarchy. |
| `impact REQUIREMENT` | Show affected requirements and tasks. |
| `audit [ENTITY]` | List audit events. |
| `render [OPTIONS]` | Generate requirement and reconciliation views. |

Human-readable output shall be the default. Each command shall support
`--json`. Invalid input shall show the error and command-specific usage. A
rendered view may select a level, hierarchy root, or reconciliation state.
