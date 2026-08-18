PRAGMA foreign_keys = ON;
PRAGMA user_version = 1;

CREATE TABLE requirement (
    id TEXT PRIMARY KEY,
    current_revision INTEGER NOT NULL CHECK (current_revision > 0),
    created_at TEXT NOT NULL,
    FOREIGN KEY (id, current_revision)
        REFERENCES requirement_revision(requirement_id, revision)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE requirement_revision (
    requirement_id TEXT NOT NULL REFERENCES requirement(id),
    revision INTEGER NOT NULL CHECK (revision > 0),
    content_json TEXT NOT NULL CHECK (json_valid(content_json)),
    content_hash TEXT NOT NULL CHECK (
        length(content_hash) = 64
        AND content_hash NOT GLOB '*[^0-9a-f]*'
    ),
    created_at TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    PRIMARY KEY (requirement_id, revision),
    UNIQUE (requirement_id, content_hash),
    CHECK (json_type(content_json) = 'object'),
    CHECK (
        COALESCE(
            json_extract(content_json, '$.id') = requirement_id,
            0
        )
    ),
    CHECK (
        COALESCE(
            json_extract(content_json, '$.revision') = revision,
            0
        )
    )
);

CREATE TRIGGER requirement_revision_is_next
BEFORE INSERT ON requirement_revision
WHEN NEW.revision != COALESCE(
    (
        SELECT MAX(revision) + 1
        FROM requirement_revision
        WHERE requirement_id = NEW.requirement_id
    ),
    1
)
BEGIN
    SELECT RAISE(ABORT, 'requirement revision is not next');
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

CREATE TRIGGER requirement_current_is_next
BEFORE UPDATE OF current_revision ON requirement
WHEN NEW.current_revision != OLD.current_revision + 1
BEGIN
    SELECT RAISE(ABORT, 'current revision is not next');
END;

CREATE VIEW current_requirement AS
SELECT revision.*
FROM requirement
JOIN requirement_revision AS revision
  ON revision.requirement_id = requirement.id
 AND revision.revision = requirement.current_revision;

CREATE VIEW requirement_refinement AS
SELECT
    revision.requirement_id AS child_id,
    revision.revision AS child_revision,
    link.value AS parent_ref
FROM requirement_revision AS revision,
     json_each(revision.content_json, '$.links.refines') AS link;

CREATE TABLE task (
    id TEXT PRIMARY KEY,
    version INTEGER NOT NULL CHECK (version > 0),
    definition_json TEXT NOT NULL CHECK (json_valid(definition_json)),
    definition_hash TEXT NOT NULL CHECK (
        length(definition_hash) = 64
        AND definition_hash NOT GLOB '*[^0-9a-f]*'
    ),
    state TEXT NOT NULL DEFAULT 'open'
        CHECK (state IN ('open', 'complete')),
    fence INTEGER NOT NULL DEFAULT 0 CHECK (fence >= 0),
    completed_definition_hash TEXT,
    completed_commit TEXT,
    completed_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK (json_type(definition_json) = 'object'),
    CHECK (
        COALESCE(json_extract(definition_json, '$.id') = id, 0)
    ),
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

CREATE VIEW task_dependency AS
SELECT task.id AS task_id, dependency.value AS dependency_id
FROM task, json_each(task.definition_json, '$.depends_on') AS dependency;

CREATE VIEW task_contribution AS
SELECT task.id AS task_id, contribution.value AS requirement_ref
FROM task,
     json_each(task.definition_json, '$.contributes_to') AS contribution;

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

CREATE TABLE evidence (
    id INTEGER PRIMARY KEY,
    requirement_id TEXT NOT NULL,
    requirement_revision INTEGER NOT NULL CHECK (requirement_revision > 0),
    kind TEXT NOT NULL CHECK (kind IN ('impl', 'test')),
    path TEXT NOT NULL,
    line INTEGER NOT NULL CHECK (line > 0),
    commit_sha TEXT NOT NULL,
    content_hash TEXT NOT NULL CHECK (
        length(content_hash) = 64
        AND content_hash NOT GLOB '*[^0-9a-f]*'
    ),
    recorded_at TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    FOREIGN KEY (requirement_id, requirement_revision)
        REFERENCES requirement_revision(requirement_id, revision),
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

CREATE TABLE audit_event (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    occurred_at TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    entity_type TEXT NOT NULL,
    entity_id TEXT NOT NULL,
    data_json TEXT NOT NULL CHECK (json_valid(data_json))
);

CREATE TRIGGER audit_event_no_update
BEFORE UPDATE ON audit_event
BEGIN
    SELECT RAISE(ABORT, 'audit event is append-only');
END;

CREATE TRIGGER audit_event_no_delete
BEFORE DELETE ON audit_event
BEGIN
    SELECT RAISE(ABORT, 'audit event is append-only');
END;
