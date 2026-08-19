PRAGMA foreign_keys = ON;
CREATE TABLE requirement (
    id TEXT PRIMARY KEY,
    current_revision INTEGER NOT NULL CHECK (current_revision > 0),
    lifecycle_state TEXT NOT NULL DEFAULT 'active'
        CHECK (lifecycle_state IN ('active', 'retired')),
    reconciliation_state TEXT NOT NULL
        CHECK (reconciliation_state IN (
            'unimplemented', 'in_progress', 'implemented',
            'needs_reconciliation'
        )),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (id, current_revision)
        REFERENCES requirement_revision(requirement_id, revision)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE requirement_revision (
    requirement_id TEXT NOT NULL REFERENCES requirement(id),
    revision INTEGER NOT NULL CHECK (revision > 0),
    level TEXT NOT NULL
        CHECK (level IN ('business', 'stakeholder', 'system', 'software')),
    title TEXT NOT NULL CHECK (length(title) > 0),
    statement TEXT NOT NULL CHECK (length(statement) > 0),
    created_at TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    PRIMARY KEY (requirement_id, revision)
);

CREATE INDEX requirement_reconciliation_state
    ON requirement(reconciliation_state, id);

CREATE INDEX requirement_revision_level
    ON requirement_revision(level, requirement_id, revision);

CREATE TABLE requirement_refinement (
    child_id TEXT NOT NULL,
    child_revision INTEGER NOT NULL,
    parent_id TEXT NOT NULL,
    parent_revision INTEGER NOT NULL,
    PRIMARY KEY (child_id, child_revision, parent_id, parent_revision),
    FOREIGN KEY (child_id, child_revision)
        REFERENCES requirement_revision(requirement_id, revision),
    FOREIGN KEY (parent_id, parent_revision)
        REFERENCES requirement_revision(requirement_id, revision),
    CHECK (child_id != parent_id)
);

CREATE INDEX refinement_parent
    ON requirement_refinement(parent_id, parent_revision);

CREATE TABLE requirement_dependency (
    requirement_id TEXT NOT NULL,
    requirement_revision INTEGER NOT NULL,
    dependency_id TEXT NOT NULL,
    dependency_revision INTEGER NOT NULL,
    PRIMARY KEY (
        requirement_id, requirement_revision,
        dependency_id, dependency_revision
    ),
    FOREIGN KEY (requirement_id, requirement_revision)
        REFERENCES requirement_revision(requirement_id, revision),
    FOREIGN KEY (dependency_id, dependency_revision)
        REFERENCES requirement_revision(requirement_id, revision),
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
    id TEXT PRIMARY KEY,
    version INTEGER NOT NULL CHECK (version > 0),
    title TEXT NOT NULL CHECK (length(title) > 0),
    description TEXT NOT NULL CHECK (length(description) > 0),
    priority INTEGER NOT NULL CHECK (priority BETWEEN 0 AND 100),
    state TEXT NOT NULL CHECK (state IN ('open', 'blocked', 'complete')),
    fence INTEGER NOT NULL DEFAULT 0 CHECK (fence >= 0),
    completed_commit TEXT,
    completed_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK (
        (state = 'complete' AND completed_commit IS NOT NULL
            AND completed_at IS NOT NULL)
        OR
        (state != 'complete' AND completed_commit IS NULL
            AND completed_at IS NULL)
    )
);

CREATE TABLE task_dependency (
    task_id TEXT NOT NULL REFERENCES task(id),
    dependency_id TEXT NOT NULL REFERENCES task(id),
    PRIMARY KEY (task_id, dependency_id),
    CHECK (task_id != dependency_id)
);

CREATE INDEX task_ready ON task(state, priority DESC, id);

CREATE INDEX task_dependency_reverse ON task_dependency(dependency_id);

CREATE TABLE task_requirement (
    task_id TEXT NOT NULL REFERENCES task(id),
    requirement_id TEXT NOT NULL,
    requirement_revision INTEGER NOT NULL,
    purpose TEXT NOT NULL CHECK (purpose IN ('implement', 'reconcile')),
    PRIMARY KEY (task_id, requirement_id, requirement_revision, purpose),
    FOREIGN KEY (requirement_id, requirement_revision)
        REFERENCES requirement_revision(requirement_id, revision)
);

CREATE INDEX task_requirement_reverse
    ON task_requirement(requirement_id, requirement_revision);

CREATE TABLE lease (
    task_id TEXT PRIMARY KEY REFERENCES task(id),
    lease_id TEXT NOT NULL UNIQUE,
    agent_id TEXT NOT NULL,
    fence INTEGER NOT NULL CHECK (fence > 0),
    claimed_at TEXT NOT NULL,
    heartbeat_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);

CREATE INDEX lease_expiry ON lease(expires_at);

CREATE TABLE pull_request (
    id INTEGER PRIMARY KEY,
    repository TEXT NOT NULL,
    number INTEGER NOT NULL CHECK (number > 0),
    url TEXT NOT NULL,
    UNIQUE (repository, number)
);

CREATE TABLE task_pull_request (
    task_id TEXT NOT NULL REFERENCES task(id),
    pull_request_id INTEGER NOT NULL REFERENCES pull_request(id),
    PRIMARY KEY (task_id, pull_request_id)
);

CREATE INDEX task_pull_request_reverse
    ON task_pull_request(pull_request_id, task_id);

CREATE TABLE reconciliation_confirmation (
    id INTEGER PRIMARY KEY,
    requirement_id TEXT NOT NULL,
    requirement_revision INTEGER NOT NULL,
    result TEXT NOT NULL
        CHECK (result IN ('code_changed', 'existing_code_confirmed')),
    commit_sha TEXT NOT NULL,
    task_id TEXT REFERENCES task(id),
    pull_request_id INTEGER REFERENCES pull_request(id),
    confirmed_at TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    note TEXT,
    FOREIGN KEY (requirement_id, requirement_revision)
        REFERENCES requirement_revision(requirement_id, revision)
);

CREATE INDEX confirmation_requirement
    ON reconciliation_confirmation(requirement_id, requirement_revision);

CREATE TABLE reconciliation_cause (
    requirement_id TEXT NOT NULL,
    requirement_revision INTEGER NOT NULL,
    cause_requirement_id TEXT NOT NULL,
    cause_revision INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    resolved_at TEXT,
    PRIMARY KEY (
        requirement_id, requirement_revision,
        cause_requirement_id, cause_revision
    ),
    FOREIGN KEY (requirement_id, requirement_revision)
        REFERENCES requirement_revision(requirement_id, revision),
    FOREIGN KEY (cause_requirement_id, cause_revision)
        REFERENCES requirement_revision(requirement_id, revision)
);

CREATE INDEX reconciliation_cause_open
    ON reconciliation_cause(requirement_id, requirement_revision, resolved_at);

CREATE TABLE audit_event (
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

CREATE TRIGGER audit_event_no_update
BEFORE UPDATE ON audit_event
BEGIN
    SELECT RAISE(ABORT, 'audit event is append-only');
END;
