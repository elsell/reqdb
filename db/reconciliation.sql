CREATE TABLE requirement_reconciled (
    id TEXT PRIMARY KEY,
    current_revision INTEGER NOT NULL CHECK (current_revision > 0),
    lifecycle_state TEXT NOT NULL DEFAULT 'active'
        CHECK (lifecycle_state IN ('active', 'retired')),
    reconciliation_state TEXT NOT NULL
        CHECK (reconciliation_state IN (
            'pending_review', 'satisfied', 'not_satisfied'
        )),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (id, current_revision)
        REFERENCES requirement_revision(requirement_id, revision)
        DEFERRABLE INITIALLY DEFERRED
);

INSERT INTO requirement_reconciled (
    id, current_revision, lifecycle_state, reconciliation_state,
    created_at, updated_at
)
SELECT id, current_revision, lifecycle_state,
       CASE
         WHEN reconciliation_state = 'satisfied' THEN 'satisfied'
         WHEN reconciliation_state = 'not_satisfied'
          AND EXISTS (
            SELECT 1 FROM requirement_review review
            WHERE review.requirement_id = requirement.id
              AND review.requirement_revision = requirement.current_revision
              AND review.verdict = 'reject'
              AND review.reviewed_at = (
                SELECT max(latest.reviewed_at)
                FROM requirement_review latest
                WHERE latest.requirement_id = requirement.id
                  AND latest.requirement_revision = requirement.current_revision
              )
          ) THEN 'not_satisfied'
         ELSE 'pending_review'
       END,
       created_at, updated_at
FROM requirement;

DROP TABLE requirement;
ALTER TABLE requirement_reconciled RENAME TO requirement;

CREATE INDEX requirement_reconciliation_state
    ON requirement(reconciliation_state, id);

UPDATE state_history
SET from_value = CASE from_value
        WHEN 'in_progress' THEN 'pending_review'
        WHEN 'ready_for_review' THEN 'pending_review'
        WHEN 'needs_reconciliation' THEN 'pending_review'
        ELSE from_value
    END,
    to_value = CASE to_value
        WHEN 'in_progress' THEN 'pending_review'
        WHEN 'ready_for_review' THEN 'pending_review'
        WHEN 'needs_reconciliation' THEN 'pending_review'
        ELSE to_value
    END
WHERE entity_type = 'requirement' AND field = 'reconciliation';
