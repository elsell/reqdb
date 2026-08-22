package postgres

import (
	"regexp"
	"strings"

	dbschema "github.com/elsell/reqdb/db"
	"github.com/elsell/reqdb/internal/store/sqlstore"
	"gorm.io/driver/postgres"
)

type Store = sqlstore.Store

var sqliteTrigger = regexp.MustCompile(`(?s)CREATE TRIGGER .*?END;\s*`)

func Open(dsn string) (*Store, error) {
	return sqlstore.Open(sqlstore.Config{
		Dialector: postgres.Open(dsn),
		Schema:    schema(),
	})
}

func schema() string {
	value := strings.TrimPrefix(dbschema.Schema, "PRAGMA foreign_keys = ON;\n")
	deferredCurrentRevision := `,
    FOREIGN KEY (project_id, id, current_revision)
        REFERENCES requirement_revision(project_id, requirement_id, revision)
        DEFERRABLE INITIALLY DEFERRED`
	value = strings.Replace(value, deferredCurrentRevision, "", 1)
	value = strings.ReplaceAll(value, "INTEGER PRIMARY KEY AUTOINCREMENT", "BIGSERIAL PRIMARY KEY")
	value = strings.Replace(value, "id INTEGER PRIMARY KEY,", "id BIGSERIAL PRIMARY KEY,", 1)
	value = strings.Replace(value, "data_json TEXT NOT NULL CHECK (json_valid(data_json))", "data_json TEXT NOT NULL CHECK (data_json::jsonb IS NOT NULL)", 1)
	value = sqliteTrigger.ReplaceAllString(value, "")
	return value + `
ALTER TABLE requirement ADD CONSTRAINT requirement_current_revision_fk
FOREIGN KEY (project_id, id, current_revision)
REFERENCES requirement_revision(project_id, requirement_id, revision)
DEFERRABLE INITIALLY DEFERRED;

CREATE FUNCTION reqdb_reject_append_only_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% is append-only', TG_TABLE_NAME
        USING ERRCODE = 'integrity_constraint_violation';
END;
$$;

CREATE TRIGGER requirement_dependency_append_only
BEFORE UPDATE OR DELETE ON requirement_dependency
FOR EACH ROW EXECUTE FUNCTION reqdb_reject_append_only_mutation();
CREATE TRIGGER requirement_refinement_append_only
BEFORE UPDATE OR DELETE ON requirement_refinement
FOR EACH ROW EXECUTE FUNCTION reqdb_reject_append_only_mutation();
CREATE TRIGGER requirement_revision_append_only
BEFORE UPDATE OR DELETE ON requirement_revision
FOR EACH ROW EXECUTE FUNCTION reqdb_reject_append_only_mutation();
CREATE TRIGGER audit_event_append_only
BEFORE UPDATE OR DELETE ON audit_event
FOR EACH ROW EXECUTE FUNCTION reqdb_reject_append_only_mutation();
`
}
