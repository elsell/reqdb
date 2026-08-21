CREATE TABLE requirement_satisfaction_new (
    id TEXT PRIMARY KEY,
    current_revision INTEGER NOT NULL CHECK (current_revision > 0),
    lifecycle_state TEXT NOT NULL DEFAULT 'active'
        CHECK (lifecycle_state IN ('active', 'retired')),
    reconciliation_state TEXT NOT NULL
        CHECK (reconciliation_state IN (
            'not_satisfied', 'in_progress', 'satisfied',
            'needs_reconciliation', 'ready_for_review'
        )),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (id, current_revision)
        REFERENCES requirement_revision(requirement_id, revision)
        DEFERRABLE INITIALLY DEFERRED
);

INSERT INTO requirement_satisfaction_new
SELECT id, current_revision, lifecycle_state,
       CASE reconciliation_state
           WHEN 'unimplemented' THEN 'not_satisfied'
           WHEN 'implemented' THEN 'satisfied'
           ELSE reconciliation_state
       END,
       created_at, updated_at
FROM requirement;

DROP TABLE requirement;
ALTER TABLE requirement_satisfaction_new RENAME TO requirement;

CREATE INDEX requirement_reconciliation_state
    ON requirement(reconciliation_state, id);

UPDATE state_history
SET from_value = CASE from_value
        WHEN 'unimplemented' THEN 'not_satisfied'
        WHEN 'implemented' THEN 'satisfied'
        ELSE from_value
    END,
    to_value = CASE to_value
        WHEN 'unimplemented' THEN 'not_satisfied'
        WHEN 'implemented' THEN 'satisfied'
        ELSE to_value
    END
WHERE entity_type = 'requirement' AND field = 'reconciliation';

CREATE TABLE requirement_review (
    id TEXT PRIMARY KEY,
    requirement_id TEXT NOT NULL,
    requirement_revision INTEGER NOT NULL,
    commit_sha TEXT NOT NULL,
    task_id TEXT REFERENCES task(id),
    verdict TEXT NOT NULL CHECK (verdict IN ('accept', 'reject')),
    reviewed_at TEXT NOT NULL,
    reviewer_id TEXT NOT NULL,
    UNIQUE (requirement_id, requirement_revision, commit_sha),
    FOREIGN KEY (requirement_id, requirement_revision)
        REFERENCES requirement_revision(requirement_id, revision)
);

CREATE INDEX review_requirement
    ON requirement_review(requirement_id, requirement_revision, reviewed_at);

INSERT OR IGNORE INTO requirement_review
    (id, requirement_id, requirement_revision, commit_sha, task_id,
     verdict, reviewed_at, reviewer_id)
SELECT 'RV-MIGRATED-' || printf('%012d', id), requirement_id,
       requirement_revision, commit_sha, task_id, 'accept', confirmed_at,
       actor_id
FROM reconciliation_confirmation;

CREATE TABLE review_finding (
    review_id TEXT NOT NULL REFERENCES requirement_review(id),
    ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
    message TEXT NOT NULL CHECK (length(trim(message)) > 0),
    path TEXT NOT NULL DEFAULT '',
    line INTEGER NOT NULL DEFAULT 0 CHECK (line >= 0),
    PRIMARY KEY (review_id, ordinal),
    CHECK (line = 0 OR length(trim(path)) > 0)
);

DROP TABLE reconciliation_confirmation;
