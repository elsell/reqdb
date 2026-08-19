CREATE TABLE requirement_workflow_new (
    id TEXT PRIMARY KEY,
    current_revision INTEGER NOT NULL CHECK (current_revision > 0),
    lifecycle_state TEXT NOT NULL DEFAULT 'active'
        CHECK (lifecycle_state IN ('active', 'retired')),
    reconciliation_state TEXT NOT NULL
        CHECK (reconciliation_state IN (
            'unimplemented', 'in_progress', 'implemented',
            'needs_reconciliation', 'ready_for_review'
        )),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (id, current_revision)
        REFERENCES requirement_revision(requirement_id, revision)
        DEFERRABLE INITIALLY DEFERRED
);

INSERT INTO requirement_workflow_new
SELECT id, current_revision, lifecycle_state, reconciliation_state,
       created_at, updated_at
FROM requirement;

DROP TABLE requirement;
ALTER TABLE requirement_workflow_new RENAME TO requirement;

CREATE INDEX requirement_reconciliation_state
    ON requirement(reconciliation_state, id);

CREATE TABLE task_workflow_new (
    id TEXT PRIMARY KEY,
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
    CHECK (
        (state = 'complete' AND completed_commit IS NOT NULL
            AND completed_at IS NOT NULL)
        OR
        (state != 'complete' AND completed_commit IS NULL
            AND completed_at IS NULL)
    )
);

INSERT INTO task_workflow_new
SELECT id, version, title, description, priority,
       CASE WHEN state = 'blocked' THEN 'closed' ELSE state END,
       fence, completed_commit, completed_at, created_at, updated_at
FROM task;

DROP TABLE task;
ALTER TABLE task_workflow_new RENAME TO task;

CREATE INDEX task_ready ON task(state, priority DESC, id);

CREATE TABLE state_history (
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

INSERT INTO state_history
    (entity_type, entity_id, field, from_value, to_value, occurred_at, actor_id)
SELECT 'requirement', id, 'lifecycle', NULL, lifecycle_state, updated_at, 'migration'
FROM requirement;

INSERT INTO state_history
    (entity_type, entity_id, field, from_value, to_value, occurred_at, actor_id)
SELECT 'requirement', id, 'reconciliation', NULL, reconciliation_state,
       updated_at, 'migration'
FROM requirement;

INSERT INTO state_history
    (entity_type, entity_id, field, from_value, to_value, occurred_at, actor_id)
SELECT 'task', id, 'state', NULL, state, updated_at, 'migration'
FROM task;
