PRAGMA foreign_keys = ON;
CREATE TABLE project (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    description TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE requirement (
    project_id TEXT NOT NULL REFERENCES project(id),
    id TEXT NOT NULL,
    current_revision INTEGER NOT NULL CHECK (current_revision > 0),
    lifecycle_state TEXT NOT NULL DEFAULT 'active'
        CHECK (lifecycle_state IN ('active', 'retired')),
    reconciliation_state TEXT
        CHECK (reconciliation_state IN (
            'pending_review', 'satisfied', 'not_satisfied'
        )),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (project_id, id),
    FOREIGN KEY (project_id, id, current_revision)
        REFERENCES requirement_revision(project_id, requirement_id, revision)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE requirement_revision (
    project_id TEXT NOT NULL REFERENCES project(id),
    requirement_id TEXT NOT NULL,
    revision INTEGER NOT NULL CHECK (revision > 0),
    level TEXT NOT NULL
        CHECK (level IN ('business', 'stakeholder', 'system')),
    title TEXT NOT NULL CHECK (length(title) > 0),
    statement TEXT NOT NULL CHECK (length(statement) > 0),
    created_at TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    PRIMARY KEY (project_id, requirement_id, revision),
    FOREIGN KEY (project_id, requirement_id) REFERENCES requirement(project_id, id)
);

CREATE INDEX requirement_reconciliation_state
    ON requirement(reconciliation_state, id);

CREATE INDEX requirement_revision_level
    ON requirement_revision(level, requirement_id, revision);

CREATE TABLE requirement_refinement (
    project_id TEXT NOT NULL REFERENCES project(id),
    child_id TEXT NOT NULL,
    child_revision INTEGER NOT NULL,
    parent_id TEXT NOT NULL,
    parent_revision INTEGER NOT NULL,
    PRIMARY KEY (project_id, child_id, child_revision, parent_id, parent_revision),
    FOREIGN KEY (project_id, child_id, child_revision)
        REFERENCES requirement_revision(project_id, requirement_id, revision),
    FOREIGN KEY (project_id, parent_id, parent_revision)
        REFERENCES requirement_revision(project_id, requirement_id, revision),
    CHECK (child_id != parent_id)
);

CREATE INDEX refinement_parent
    ON requirement_refinement(parent_id, parent_revision);

CREATE TABLE requirement_dependency (
    project_id TEXT NOT NULL REFERENCES project(id),
    requirement_id TEXT NOT NULL,
    requirement_revision INTEGER NOT NULL,
    dependency_id TEXT NOT NULL,
    dependency_revision INTEGER NOT NULL,
    PRIMARY KEY (
        project_id, requirement_id, requirement_revision,
        dependency_id, dependency_revision
    ),
    FOREIGN KEY (project_id, requirement_id, requirement_revision)
        REFERENCES requirement_revision(project_id, requirement_id, revision),
    FOREIGN KEY (project_id, dependency_id, dependency_revision)
        REFERENCES requirement_revision(project_id, requirement_id, revision),
    CHECK (requirement_id != dependency_id)
);

CREATE INDEX requirement_dependency_reverse
    ON requirement_dependency(dependency_id, dependency_revision);

CREATE TRIGGER requirement_dependency_no_update
BEFORE UPDATE ON requirement_dependency
BEGIN
    SELECT RAISE(ABORT, 'requirement dependency is append-only');
END;

CREATE TRIGGER requirement_dependency_no_delete
BEFORE DELETE ON requirement_dependency
BEGIN
    SELECT RAISE(ABORT, 'requirement dependency is append-only');
END;

CREATE TRIGGER requirement_refinement_no_update
BEFORE UPDATE ON requirement_refinement
BEGIN
    SELECT RAISE(ABORT, 'requirement refinement is append-only');
END;

CREATE TRIGGER requirement_refinement_no_delete
BEFORE DELETE ON requirement_refinement
BEGIN
    SELECT RAISE(ABORT, 'requirement refinement is append-only');
END;

CREATE TRIGGER requirement_revision_no_update
BEFORE UPDATE ON requirement_revision
BEGIN
    SELECT RAISE(ABORT, 'requirement revision is append-only');
END;

CREATE TRIGGER requirement_revision_no_delete
BEFORE DELETE ON requirement_revision
BEGIN
    SELECT RAISE(ABORT, 'requirement revision is append-only');
END;

CREATE TABLE task (
    project_id TEXT NOT NULL REFERENCES project(id),
    id TEXT NOT NULL,
    version INTEGER NOT NULL CHECK (version > 0),
    title TEXT NOT NULL CHECK (length(title) > 0),
    description TEXT NOT NULL CHECK (length(description) > 0),
    priority INTEGER NOT NULL CHECK (priority BETWEEN 0 AND 100),
    state TEXT NOT NULL CHECK (state IN ('open', 'complete', 'closed')),
    fence INTEGER NOT NULL DEFAULT 0 CHECK (fence >= 0),
    completed_commit TEXT,
    completed_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (project_id, id),
    CHECK (
        (state = 'complete' AND completed_commit IS NOT NULL
            AND completed_at IS NOT NULL)
        OR
        (state != 'complete' AND completed_commit IS NULL
            AND completed_at IS NULL)
    )
);

CREATE TABLE task_dependency (
    project_id TEXT NOT NULL REFERENCES project(id),
    task_id TEXT NOT NULL,
    dependency_id TEXT NOT NULL,
    PRIMARY KEY (project_id, task_id, dependency_id),
    FOREIGN KEY (project_id, task_id) REFERENCES task(project_id, id),
    FOREIGN KEY (project_id, dependency_id) REFERENCES task(project_id, id),
    CHECK (task_id != dependency_id)
);

CREATE INDEX task_ready ON task(state, priority DESC, id);

CREATE INDEX task_dependency_reverse ON task_dependency(dependency_id);

CREATE TABLE task_requirement (
    project_id TEXT NOT NULL REFERENCES project(id),
    task_id TEXT NOT NULL,
    requirement_id TEXT NOT NULL,
    requirement_revision INTEGER NOT NULL,
    purpose TEXT NOT NULL CHECK (purpose IN ('implement', 'reconcile')),
    PRIMARY KEY (project_id, task_id, requirement_id, requirement_revision, purpose),
    FOREIGN KEY (project_id, task_id) REFERENCES task(project_id, id),
    FOREIGN KEY (project_id, requirement_id, requirement_revision)
        REFERENCES requirement_revision(project_id, requirement_id, revision)
);

CREATE INDEX task_requirement_reverse
    ON task_requirement(requirement_id, requirement_revision);

CREATE TABLE lease (
    project_id TEXT NOT NULL REFERENCES project(id),
    task_id TEXT NOT NULL,
    lease_id TEXT NOT NULL UNIQUE,
    agent_id TEXT NOT NULL,
    fence INTEGER NOT NULL CHECK (fence > 0),
    claimed_at TEXT NOT NULL,
    heartbeat_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    PRIMARY KEY (project_id, task_id),
    FOREIGN KEY (project_id, task_id) REFERENCES task(project_id, id)
);

CREATE INDEX lease_expiry ON lease(expires_at);

CREATE TABLE pull_request (
    project_id TEXT NOT NULL REFERENCES project(id),
    id INTEGER PRIMARY KEY,
    repository TEXT NOT NULL,
    number INTEGER NOT NULL CHECK (number > 0),
    url TEXT NOT NULL,
    UNIQUE (project_id, repository, number)
);

CREATE TABLE task_pull_request (
    project_id TEXT NOT NULL REFERENCES project(id),
    task_id TEXT NOT NULL,
    pull_request_id INTEGER NOT NULL REFERENCES pull_request(id),
    PRIMARY KEY (project_id, task_id, pull_request_id),
    FOREIGN KEY (project_id, task_id) REFERENCES task(project_id, id)
);

CREATE INDEX task_pull_request_reverse
    ON task_pull_request(pull_request_id, task_id);

CREATE TABLE requirement_review (
    project_id TEXT NOT NULL REFERENCES project(id),
    id TEXT NOT NULL,
    requirement_id TEXT NOT NULL,
    requirement_revision INTEGER NOT NULL,
    commit_sha TEXT NOT NULL,
    task_id TEXT,
    verdict TEXT NOT NULL CHECK (verdict IN ('accept', 'reject')),
    reviewed_at TEXT NOT NULL,
    reviewer_id TEXT NOT NULL,
    PRIMARY KEY (project_id, id),
    UNIQUE (project_id, requirement_id, requirement_revision, commit_sha),
    FOREIGN KEY (project_id, requirement_id, requirement_revision)
        REFERENCES requirement_revision(project_id, requirement_id, revision),
    FOREIGN KEY (project_id, task_id) REFERENCES task(project_id, id)
);

CREATE INDEX review_requirement
    ON requirement_review(requirement_id, requirement_revision, reviewed_at);

CREATE TABLE review_finding (
    project_id TEXT NOT NULL REFERENCES project(id),
    review_id TEXT NOT NULL,
    ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
    message TEXT NOT NULL CHECK (length(trim(message)) > 0),
    path TEXT NOT NULL DEFAULT '',
    line INTEGER NOT NULL DEFAULT 0 CHECK (line >= 0),
    PRIMARY KEY (project_id, review_id, ordinal),
    FOREIGN KEY (project_id, review_id) REFERENCES requirement_review(project_id, id),
    CHECK (line = 0 OR length(trim(path)) > 0)
);

CREATE TABLE reconciliation_cause (
    project_id TEXT NOT NULL REFERENCES project(id),
    requirement_id TEXT NOT NULL,
    requirement_revision INTEGER NOT NULL,
    cause_requirement_id TEXT NOT NULL,
    cause_revision INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    resolved_at TEXT,
    PRIMARY KEY (
        project_id, requirement_id, requirement_revision,
        cause_requirement_id, cause_revision
    ),
    FOREIGN KEY (project_id, requirement_id, requirement_revision)
        REFERENCES requirement_revision(project_id, requirement_id, revision),
    FOREIGN KEY (project_id, cause_requirement_id, cause_revision)
        REFERENCES requirement_revision(project_id, requirement_id, revision)
);

CREATE INDEX reconciliation_cause_open
    ON reconciliation_cause(requirement_id, requirement_revision, resolved_at);

CREATE TABLE audit_event (
    project_id TEXT NOT NULL REFERENCES project(id),
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    occurred_at TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    correlation_id TEXT NOT NULL,
    causation_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    entity_type TEXT NOT NULL,
    entity_id TEXT NOT NULL,
    data_json TEXT NOT NULL CHECK (json_valid(data_json))
);

CREATE INDEX audit_event_entity
    ON audit_event(entity_type, entity_id, sequence);

CREATE TABLE state_history (
    project_id TEXT NOT NULL REFERENCES project(id),
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_type TEXT NOT NULL CHECK (entity_type IN ('requirement', 'task')),
    entity_id TEXT NOT NULL,
    field TEXT NOT NULL,
    from_value TEXT,
    to_value TEXT NOT NULL,
    occurred_at TEXT NOT NULL,
    actor_id TEXT NOT NULL
);

CREATE INDEX state_history_entity
    ON state_history(entity_type, entity_id, sequence);

CREATE TRIGGER audit_event_no_update
BEFORE UPDATE ON audit_event
BEGIN
    SELECT RAISE(ABORT, 'audit event is append-only');
END;
