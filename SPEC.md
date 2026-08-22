# Version 1 specification

This file is normative. `shall` marks a rule.

## Purpose

`reqdb` shall track requirements and their implementation in code. It shall
also coordinate the tasks that implement or reconcile those requirements.

Version 1 shall manage requirements, traceability, reconciliation, tasks,
leases, pull request links, audit events, rendered views, and a live graph
view. It shall not run tests, manage development constraints, or manage agent
workflows.

## Authority

- SQLite or PostgreSQL shall be the system of record.
- One server shall own the selected database.
- All CLI commands shall use the server.
- YAML shall be an input format only. The CLI shall also accept resource
  attributes as options.
- Requirement revisions and audit events shall be append-only.
- Rendered files shall not be authoritative.

The server shall provide an embedded UI for the requirement and task trees.
The UI shall read the API and use an event stream to detect changes. Event
notices shall not contain authoritative graph data.

## Requirement hierarchy

| Level | View | ID prefix | Meaning | Parent |
|---|---|---|---|---|
| Business | BRS | `BR-` | A result that the organization wants. | None |
| Stakeholder | StRS | `STR-` | An outcome that a stakeholder needs. | Business |
| System | SyRS | `SYR-` | Observable system behavior or a measurable system property. | Stakeholder |

Each non-business requirement shall refine one or more requirements at the
immediate parent level. Each link shall name a parent revision. The refinement
graph shall be acyclic.

An active stakeholder requirement with no active system child shall be an
actionable leaf. An active system requirement shall also be an actionable
leaf. Only actionable leaves can have tasks, reviews, or direct stored
reconciliation state. A business requirement shall not be actionable.

A stakeholder requirement can omit system children. A system requirement
cannot have requirement children. When the first system child is added, the
server shall reject the change if the current stakeholder revision has tasks
or reviews. The operator can create a new stakeholder revision before
decomposition. When the last active system child is retired, the stakeholder
requirement shall become actionable and `pending_review`.

An active non-leaf requirement shall derive its reconciliation state from its
active current refinement leaves. It shall be `not_satisfied` when any leaf is
`not_satisfied`. Otherwise, it shall be `pending_review` when any leaf is
`pending_review`. Otherwise, it shall be `satisfied`. A retired child shall not
affect this roll-up.

Roll-up shall be calculated when reqdb reads or evaluates a requirement. Reqdb
shall not store roll-up changes as reconciliation state history.

A requirement can depend on other requirement revisions. `refines` shall
describe the requirement hierarchy. `depends_on` shall describe implementation
order. These links shall be separate. The dependency graph shall be acyclic.

An input requirement shall have one objectively verifiable obligation. Its
statement shall contain one lowercase `shall`.

## Revisions

A requirement shall have a stable ID and immutable revisions. A content change
shall create the next revision. The database shall identify one current
revision.

`requirement get` shall show all revisions in revision order. It shall also
show lifecycle and reconciliation state history in event order. List commands
shall return only the current summary.

`apply` shall use the expected current revision. A mismatch shall cause no
change.

## Lifecycle

A requirement shall be `active` or `retired`. Retirement shall preserve all
revisions, links, tasks, reviews, and audit events. It shall not create a
content revision.

When a requirement is retired, the server shall set all active, transitive
downstream requirements to `pending_review`. It shall follow refinement and
dependency links. It shall record the retired requirement as the cause. It
shall not retire downstream requirements.

A retired requirement cannot be revised or reviewed. A task that links to a
retired requirement, or depends on a retired requirement, shall not be workable or
leaseable.

Reqdb shall report workability with a `workable` Boolean, one derived
`work_status`, and `reasons`. The first matching requirement rule shall win:

1. `inactive`: The requirement is retired.
2. `managed_through_children`: The requirement is business level, or it is a
   stakeholder requirement with active system children.
3. `waiting`: A requirement dependency is not `satisfied`, or linked open
   tasks exist while none has an active lease and none is ready to lease.
4. `no_work_required`: The requirement is `satisfied`.
5. `ready_for_work`: One or more linked open tasks have no active lease.
6. `work_in_progress`: A linked task has an active lease.
7. `awaiting_review`: The requirement is `pending_review`, no linked task is
   open, and no linked task has an active lease.
8. `needs_task`: The requirement is `not_satisfied` and no linked task is open.

All task checks shall use tasks linked to the current requirement revision. A
requirement shall be workable only when its work status is `ready_for_work`.

## Reconciliation

Reconciliation describes the relation between a requirement hierarchy and the
code. Reqdb shall expose one reconciliation state for each requirement. An
actionable leaf shall use direct reconciliation. Other requirements shall use
refinement roll-up.

| State | Meaning |
|---|---|
| `pending_review` | No valid review result exists for the current revision. |
| `satisfied` | A leaf has an accepted review, or every active child of a non-leaf is recursively satisfied. |
| `not_satisfied` | A leaf has a rejected review, or a non-leaf has a not-satisfied active leaf. |

A review shall be an immutable record for one current actionable leaf revision
and one full Git commit. It shall contain a server-generated ID, the
reviewer, the review time, and the verdict `accept` or `reject`.

Each review response shall include the exact requirement ID and revision. The
API shall return one review by its stable ID. It shall list reviews for one
requirement in review time and ID order with cursor pagination. A revision in
the requirement reference shall limit the list to that revision.

A rejected review shall contain one or more findings. Each finding shall have a
message. It can have a path and positive line. Findings shall use normalized
rows, not raw JSON.

Task completion shall not satisfy a requirement by itself. A review can refer
to the completed task that produced the commit. That task shall link to the
exact requirement revision, and its completion commit shall match the review.

A completed corrective task shall change `not_satisfied` to `pending_review`
for each linked current actionable leaf. Task leasing, lease release,
heartbeat, and lease expiry shall not change reconciliation. An accepted
review shall set the current requirement revision to `satisfied` and resolve
its open reconciliation causes. A rejected review shall set it to
`not_satisfied`. Only reviews can set `satisfied` or `not_satisfied`.

A review without a task shall be valid for a current actionable leaf. The
server shall make review creation idempotent by requirement
ID, revision, and commit. An identical submission shall return the stored
review. Different content for the same key shall cause a conflict.

`requirement get` shall show its workability, work status, and reasons. The
reasons shall include state, active tasks, stale dependencies, retired
dependencies, and dependencies that are not satisfied.

When a requirement gets a new revision, the server shall:

1. Set the new current revision to `pending_review`.
2. Find all transitive downstream requirements through refinement and
   dependency links.
3. Set each downstream requirement to `pending_review`.
4. Record the changed requirement as an unresolved cause for each affected
   requirement.

## Tasks and leases

A task shall have a title, description, priority, state, requirement links,
and task dependencies. A requirement link shall have the purpose `implement`
or `reconcile`.

An implementation or reconciliation task shall link only to actionable leaf
revisions.

A task shall be `open`, `complete`, or `closed`. Closing shall stop an open,
unleased task without completing it. A closed task shall not satisfy task
dependencies and shall not prevent creation of replacement work for a linked
requirement.

A task is workable when all these conditions are true:

- The task is open.
- The task has no active lease.
- All task dependencies are complete.
- All transitive dependencies of its linked requirements are current and
  `satisfied`.

The lease operation shall check the same conditions in its transaction. Workable
tasks shall sort by descending priority and then by ID.

Reqdb shall use the same workability shape for tasks. It shall compute one task
work status:

- `ready_to_lease`: The task is workable.
- `work_in_progress`: An active lease covers the task.
- `waiting`: A dependency or linked requirement must change first.
- `complete`: The task is complete.
- `closed`: The task is closed.

A claim shall create one lease in one transaction. A lease shall identify the
task, agent, fence, claim time, and expiry time. Claim, heartbeat, release, and
completion operations shall use the current lease and fence.

The server shall schedule the next lease expiry and process it promptly after
its expiry time. Lease changes shall reschedule this work. Expiry shall remove
the active lease and record the lease expiry.

The server shall list active leases in ascending lease ID order. The list shall
support cursor pagination and optional agent and task filters. Expired leases
shall not appear. Their history shall remain available in the audit log.

A completed task shall record its full 40-character hexadecimal Git commit.
A task can link to zero or more pull requests. A pull request can link to more
than one task. Task detail output shall show all pull request links.

`task get` shall show its state history, workability, work status, and reasons.

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
- Requirement check, create, and update commands and task create commands shall
  accept resource attributes as CLI options.
- These commands shall also accept YAML from a file or from standard input.
- A command shall not combine YAML input with resource attribute options.
- Refinement, requirement dependency, task requirement, and task dependency
  options shall be repeatable.
- File input and option input shall use the same API and domain validation.

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
| `requirement workable` | List requirements that permit new work. |
| `requirement get ID[@REVISION]` | Show one requirement revision. |
| `requirement check [ID] [OPTIONS]` | Validate one requirement input. |
| `requirement create [ID] [OPTIONS]` | Create a requirement. |
| `requirement update ID [OPTIONS]` | Create a requirement revision. |
| `requirement review ID[@REVISION] --commit SHA --verdict VERDICT` | Accept or reject code for a revision. |
| `review get ID` | Show one immutable review. |
| `review list REQUIREMENT[@REVISION]` | List reviews for one requirement. |
| `requirement retire ID` | Retire a requirement without deleting its history. |
| `requirement render ID` | Render one requirement and its context. |
| `task list` | List tasks. |
| `task get ID` | Show one task. |
| `task create [OPTIONS]` | Create a task. |
| `task workable` | List tasks that are ready to lease. |
| `task lease ID --agent ID` | Lease one task. |
| `task complete ID --lease ID --fence N --commit SHA` | Complete one task. |
| `task close ID` | Close an open, unleased task. |
| `task link-pr ID --url URL` | Link a task to a pull request. |
| `lease list [--agent ID] [--task ID]` | List active leases. |
| `lease heartbeat ID --fence N` | Extend one lease. |
| `lease release ID --fence N` | Release one lease. |

Server-wide and graph-wide operations shall remain top-level commands:

| Command | Result |
|---|---|
| `serve` | Own the selected database and serve clients. |
| `trace [REQUIREMENT]` | Show the refinement hierarchy and separate dependency links. |
| `impact REQUIREMENT` | Follow refinement and dependency links to show affected requirements and tasks. |
| `audit [ENTITY]` | List audit events. |
| `render [OPTIONS]` | Generate requirement and reconciliation views. |

Human-readable output shall be the default. Each command shall support
`--json`. Invalid input shall show the error and command-specific usage. A
rendered view may select a level, hierarchy root, or reconciliation state.
