package sqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dbschema "github.com/elsell/reqdb/db"
	"github.com/elsell/reqdb/internal/domain"
	"github.com/elsell/reqdb/internal/store/sqlite"
	_ "github.com/mattn/go-sqlite3"
)

func requirement(id, level string, revision int, parents ...string) domain.RequirementInput {
	input := domain.RequirementInput{Schema: "requirement/v1", ID: id, Level: level, Revision: revision, Title: id, Statement: fmt.Sprintf("The %s shall provide one result.", level)}
	input.Links.Refines = parents
	return input
}

func TestRequirementReconciliationAndTaskLease(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "reqdb.sqlite")
	store, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	inputs := []domain.RequirementInput{
		requirement("BR-TEST-001", "business", 1),
		requirement("STR-TEST-001", "stakeholder", 1, "BR-TEST-001@1"),
		requirement("SYR-TEST-001", "system", 1, "STR-TEST-001@1"),
		requirement("SWR-TEST-001", "software", 1, "SYR-TEST-001@1"),
	}
	for _, input := range inputs {
		if _, err := store.CreateRequirement(ctx, input, "tester"); err != nil {
			t.Fatalf("create %s: %v", input.ID, err)
		}
		if _, err := store.ConfirmRequirement(ctx, domain.RequirementRef{ID: input.ID}, "abc123", "code_changed", "tester"); err != nil {
			t.Fatalf("confirm %s: %v", input.ID, err)
		}
	}

	root := requirement("BR-TEST-001", "business", 2)
	if _, err := store.UpdateRequirement(ctx, root, 1, "tester"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"STR-TEST-001", "SYR-TEST-001", "SWR-TEST-001"} {
		item, err := store.GetRequirement(ctx, domain.RequirementRef{ID: id})
		if err != nil {
			t.Fatal(err)
		}
		if item.ReconciliationState != domain.NeedsReconciliation {
			t.Fatalf("%s state is %s", id, item.ReconciliationState)
		}
	}

	taskInput := domain.TaskInput{Schema: "task/v1", ID: "T-1", Title: "Reconcile", Description: "Reconcile the software requirement.", Priority: 50, Requirements: []domain.TaskRequirementInput{{Requirement: "SWR-TEST-001@1", Purpose: "reconcile"}}}
	if _, err := store.CreateTask(ctx, taskInput, "tester"); err != nil {
		t.Fatal(err)
	}
	linkedTasks, err := store.TasksForRequirements(ctx, []domain.RequirementRef{{ID: "SWR-TEST-001", Revision: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(linkedTasks) != 1 || linkedTasks[0].ID != "T-1" {
		t.Fatalf("unexpected linked tasks: %+v", linkedTasks)
	}
	lease, err := store.LeaseTask(ctx, "T-1", "agent-1", time.Minute, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteTask(ctx, "T-1", lease.LeaseID, lease.Fence, "def456", "tester"); err != nil {
		t.Fatal(err)
	}
	item, err := store.GetRequirement(ctx, domain.RequirementRef{ID: "SWR-TEST-001"})
	if err != nil {
		t.Fatal(err)
	}
	if item.ReconciliationState != domain.NeedsReconciliation {
		t.Fatalf("state is %s", item.ReconciliationState)
	}
	if _, err := store.ConfirmRequirement(ctx, domain.RequirementRef{ID: "SWR-TEST-001"}, "def456", "code_changed", "tester"); err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseCanReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reqdb.sqlite")
	first, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = second.Close()
}

func TestOpenMigratesVersionOneDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reqdb.sqlite")
	legacy := strings.Replace(dbschema.Schema, "    correlation_id TEXT NOT NULL,\n", "", 1)
	legacy = strings.Replace(legacy, "    causation_id TEXT NOT NULL,\n", "", 1)
	legacy += `
CREATE TRIGGER audit_event_no_delete
BEFORE DELETE ON audit_event
BEGIN
    SELECT RAISE(ABORT, 'audit event is append-only');
END;
`
	database, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(legacy); err != nil {
		t.Fatal(err)
	}
	_ = database.Close()

	store, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	database, err = sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var migrations int
	if err := database.QueryRow(`SELECT count(*) FROM schema_migrations WHERE id IN ('SCHEMA_INIT', '202608180001')`).Scan(&migrations); err != nil {
		t.Fatal(err)
	}
	if migrations != 2 {
		t.Fatalf("database recorded %d expected migrations", migrations)
	}
	var columns int
	if err := database.QueryRow(`SELECT count(*) FROM pragma_table_info('audit_event') WHERE name IN ('correlation_id','causation_id')`).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if columns != 2 {
		t.Fatalf("migration added %d request ID columns", columns)
	}
	var triggers int
	if err := database.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name='audit_event_no_delete'`).Scan(&triggers); err != nil {
		t.Fatal(err)
	}
	if triggers != 0 {
		t.Fatal("migration did not remove the audit delete trigger")
	}
}

func TestDuplicateRequirementHasFriendlyError(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "reqdb.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	input := requirement("BR-TEST-001", "business", 1)
	if _, err := store.CreateRequirement(context.Background(), input, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRequirement(context.Background(), input, "tester"); err == nil || err.Error() != "requirement BR-TEST-001 already exists" {
		t.Fatalf("unexpected duplicate error: %v", err)
	}
}

func TestTaskWithMissingRequirementHasFriendlyError(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "reqdb.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	input := domain.TaskInput{
		Schema:      "task/v1",
		ID:          "T-1",
		Title:       "Implement",
		Description: "Implement the missing requirement.",
		Priority:    50,
		Requirements: []domain.TaskRequirementInput{
			{Requirement: "SWR-MISSING-001@1", Purpose: "implement"},
		},
	}
	_, err = store.CreateTask(context.Background(), input, "tester")
	if err == nil || err.Error() != "requirement SWR-MISSING-001@1 does not exist" {
		t.Fatalf("unexpected missing requirement error: %v", err)
	}
}
