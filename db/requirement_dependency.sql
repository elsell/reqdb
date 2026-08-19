CREATE TABLE IF NOT EXISTS requirement_dependency (
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

CREATE INDEX IF NOT EXISTS requirement_dependency_reverse
    ON requirement_dependency(dependency_id, dependency_revision);

CREATE TRIGGER IF NOT EXISTS requirement_dependency_no_update
BEFORE UPDATE ON requirement_dependency
BEGIN
    SELECT RAISE(ABORT, 'requirement dependency is append-only');
END;

CREATE TRIGGER IF NOT EXISTS requirement_dependency_no_delete
BEFORE DELETE ON requirement_dependency
BEGIN
    SELECT RAISE(ABORT, 'requirement dependency is append-only');
END;
