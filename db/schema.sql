PRAGMA foreign_keys = ON;
PRAGMA user_version = 1;

CREATE TABLE project (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    project_id TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL
);

CREATE TABLE task_state (
    task_id TEXT PRIMARY KEY,
    definition_hash TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'open'
        CHECK (state IN ('open', 'complete')),
    fence INTEGER NOT NULL DEFAULT 0 CHECK (fence >= 0),
    completed_definition_hash TEXT,
    completed_commit TEXT,
    completed_at TEXT,
    updated_at TEXT NOT NULL,
    CHECK (
        (state = 'complete'
            AND completed_definition_hash IS NOT NULL
            AND completed_commit IS NOT NULL
            AND completed_at IS NOT NULL)
        OR
        (state = 'open'
            AND completed_definition_hash IS NULL
            AND completed_commit IS NULL
            AND completed_at IS NULL)
    )
);

CREATE TABLE lease (
    task_id TEXT PRIMARY KEY REFERENCES task_state(task_id) ON DELETE CASCADE,
    lease_id TEXT NOT NULL UNIQUE,
    agent_id TEXT NOT NULL,
    fence INTEGER NOT NULL CHECK (fence > 0),
    claimed_at TEXT NOT NULL,
    heartbeat_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);

CREATE INDEX lease_expiry ON lease(expires_at);

CREATE TABLE evidence (
    id INTEGER PRIMARY KEY,
    requirement_id TEXT NOT NULL,
    requirement_revision INTEGER NOT NULL CHECK (requirement_revision > 0),
    kind TEXT NOT NULL CHECK (kind IN ('impl', 'test')),
    path TEXT NOT NULL,
    line INTEGER NOT NULL CHECK (line > 0),
    commit_sha TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    UNIQUE (
        requirement_id,
        requirement_revision,
        kind,
        path,
        line,
        commit_sha
    )
);

CREATE INDEX evidence_requirement
    ON evidence(requirement_id, requirement_revision);

CREATE TABLE event (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    occurred_at TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (
        kind IN (
            'task_reopened',
            'lease_claimed',
            'lease_released',
            'lease_reclaimed',
            'check_failed',
            'task_completed'
        )
    ),
    entity_id TEXT NOT NULL,
    data TEXT NOT NULL CHECK (json_valid(data))
);
