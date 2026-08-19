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

func TestListActiveLeasesWithFiltersAndPagination(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "reqdb.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, id := range []string{"T-1", "T-2"} {
		input := domain.TaskInput{Schema: "task/v1", ID: id, Title: id, Description: "Perform the implementation work.", Priority: 1}
		if _, err := store.CreateTask(ctx, input, "tester"); err != nil {
			t.Fatal(err)
		}
	}
	first, err := store.LeaseTask(ctx, "T-1", "agent-a", time.Minute, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LeaseTask(ctx, "T-2", "agent-b", time.Minute, "tester"); err != nil {
		t.Fatal(err)
	}

	page, err := store.ListLeases(ctx, "", 1, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatalf("unexpected first page: %+v", page)
	}
	page, err = store.ListLeases(ctx, page.NextCursor, 1, "", "")
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("unexpected second page: %+v, %v", page, err)
	}
	page, err = store.ListLeases(ctx, "", 10, "agent-a", "T-1")
	if err != nil || len(page.Items) != 1 || page.Items[0].LeaseID != first.LeaseID {
		t.Fatalf("unexpected filtered leases: %+v, %v", page, err)
	}
	if err := store.Release(ctx, first.LeaseID, first.Fence, "tester"); err != nil {
		t.Fatal(err)
	}
	page, err = store.ListLeases(ctx, "", 10, "", "T-1")
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("released lease is still active: %+v, %v", page, err)
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
	if err := database.QueryRow(`SELECT count(*) FROM schema_migrations WHERE id IN ('SCHEMA_INIT', '202608180001', '202608180002', '202608180003')`).Scan(&migrations); err != nil {
		t.Fatal(err)
	}
	if migrations != 4 {
		t.Fatalf("database recorded %d expected migrations", migrations)
	}
	var dependencyTables int
	if err := database.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='requirement_dependency'`).Scan(&dependencyTables); err != nil {
		t.Fatal(err)
	}
	if dependencyTables != 1 {
		t.Fatal("migration did not add the requirement dependency table")
	}
	var lifecycleColumns int
	if err := database.QueryRow(`SELECT count(*) FROM pragma_table_info('requirement') WHERE name='lifecycle_state'`).Scan(&lifecycleColumns); err != nil {
		t.Fatal(err)
	}
	if lifecycleColumns != 1 {
		t.Fatal("migration did not add requirement lifecycle state")
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

func TestRequirementDependenciesBlockTaskUntilImplemented(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "reqdb.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	prerequisite := requirement("SWR-BASE-001", "software", 1)
	target := requirement("SWR-TARGET-001", "software", 1)
	target.Links.DependsOn = []string{"SWR-BASE-001@1"}
	for _, input := range []domain.RequirementInput{prerequisite, target} {
		if _, err := store.CreateRequirement(ctx, input, "tester"); err != nil {
			t.Fatal(err)
		}
	}
	stored, err := store.GetRequirement(ctx, domain.RequirementRef{ID: target.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Revision.Dependencies) != 1 || stored.Revision.Dependencies[0].ID != prerequisite.ID {
		t.Fatalf("unexpected dependencies: %+v", stored.Revision.Dependencies)
	}
	task := domain.TaskInput{Schema: "task/v1", ID: "T-DEPENDENT", Title: "Implement target", Description: "Implement the target requirement.", Priority: 50, Requirements: []domain.TaskRequirementInput{{Requirement: "SWR-TARGET-001@1", Purpose: "implement"}}}
	if _, err := store.CreateTask(ctx, task, "tester"); err != nil {
		t.Fatal(err)
	}
	ready, err := store.ListTasks(ctx, "", 20, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(ready.Items) != 0 {
		t.Fatalf("blocked task is ready: %+v", ready.Items)
	}
	if _, err := store.LeaseTask(ctx, task.ID, "agent", time.Minute, "tester"); err == nil || !strings.Contains(err.Error(), "requirement dependencies") {
		t.Fatalf("unexpected lease error: %v", err)
	}
	if _, err := store.ConfirmRequirement(ctx, domain.RequirementRef{ID: prerequisite.ID}, "abc123", "code_changed", "tester"); err != nil {
		t.Fatal(err)
	}
	ready, err = store.ListTasks(ctx, "", 20, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(ready.Items) != 1 || ready.Items[0].ID != task.ID {
		t.Fatalf("implemented prerequisite did not unblock task: %+v", ready.Items)
	}
	if _, err := store.LeaseTask(ctx, task.ID, "agent", time.Minute, "tester"); err != nil {
		t.Fatal(err)
	}
}

func TestRequirementRevisionInvalidatesDependencyConsumers(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "reqdb.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	base := requirement("SWR-BASE-001", "software", 1)
	consumer := requirement("SWR-CONSUMER-001", "software", 1)
	consumer.Links.DependsOn = []string{"SWR-BASE-001@1"}
	for _, input := range []domain.RequirementInput{base, consumer} {
		if _, err := store.CreateRequirement(ctx, input, "tester"); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ConfirmRequirement(ctx, domain.RequirementRef{ID: input.ID}, "abc123", "code_changed", "tester"); err != nil {
			t.Fatal(err)
		}
	}
	base.Revision = 2
	if _, err := store.UpdateRequirement(ctx, base, 1, "tester"); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetRequirement(ctx, domain.RequirementRef{ID: consumer.ID})
	if err != nil {
		t.Fatal(err)
	}
	if stored.ReconciliationState != domain.NeedsReconciliation {
		t.Fatalf("consumer state is %s", stored.ReconciliationState)
	}
}

func TestTransitiveRequirementDependenciesBlockTask(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "reqdb.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	base := requirement("SWR-BASE-001", "software", 1)
	middle := requirement("SWR-MIDDLE-001", "software", 1)
	middle.Links.DependsOn = []string{"SWR-BASE-001@1"}
	target := requirement("SWR-TARGET-001", "software", 1)
	target.Links.DependsOn = []string{"SWR-MIDDLE-001@1"}
	for _, input := range []domain.RequirementInput{base, middle, target} {
		if _, err := store.CreateRequirement(ctx, input, "tester"); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{middle.ID} {
		if _, err := store.ConfirmRequirement(ctx, domain.RequirementRef{ID: id}, "abc123", "code_changed", "tester"); err != nil {
			t.Fatal(err)
		}
	}
	task := domain.TaskInput{Schema: "task/v1", ID: "T-TRANSITIVE", Title: "Implement target", Description: "Implement the target requirement.", Priority: 50, Requirements: []domain.TaskRequirementInput{{Requirement: "SWR-TARGET-001@1", Purpose: "implement"}}}
	if _, err := store.CreateTask(ctx, task, "tester"); err != nil {
		t.Fatal(err)
	}
	ready, err := store.ListTasks(ctx, "", 20, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(ready.Items) != 0 {
		t.Fatalf("transitively blocked task is ready: %+v", ready.Items)
	}
}

func TestRetireRequirementInvalidatesDownstreamAndBlocksTasks(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "reqdb.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	base := requirement("SWR-BASE-001", "software", 1)
	consumer := requirement("SWR-CONSUMER-001", "software", 1)
	consumer.Links.DependsOn = []string{"SWR-BASE-001@1"}
	for _, input := range []domain.RequirementInput{base, consumer} {
		if _, err := store.CreateRequirement(ctx, input, "tester"); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ConfirmRequirement(ctx, domain.RequirementRef{ID: input.ID}, "abc123", "code_changed", "tester"); err != nil {
			t.Fatal(err)
		}
	}
	for _, task := range []domain.TaskInput{
		{Schema: "task/v1", ID: "T-BASE", Title: "Change base", Description: "Change the base requirement.", Priority: 50, Requirements: []domain.TaskRequirementInput{{Requirement: "SWR-BASE-001@1", Purpose: "reconcile"}}},
		{Schema: "task/v1", ID: "T-CONSUMER", Title: "Change consumer", Description: "Change the consumer requirement.", Priority: 50, Requirements: []domain.TaskRequirementInput{{Requirement: "SWR-CONSUMER-001@1", Purpose: "reconcile"}}},
	} {
		if _, err := store.CreateTask(ctx, task, "tester"); err != nil {
			t.Fatal(err)
		}
	}
	retired, err := store.RetireRequirement(ctx, base.ID, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if retired.LifecycleState != domain.Retired {
		t.Fatalf("lifecycle state is %s", retired.LifecycleState)
	}
	stored, err := store.GetRequirement(ctx, domain.RequirementRef{ID: consumer.ID})
	if err != nil {
		t.Fatal(err)
	}
	if stored.ReconciliationState != domain.NeedsReconciliation {
		t.Fatalf("consumer state is %s", stored.ReconciliationState)
	}
	ready, err := store.ListTasks(ctx, "", 20, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(ready.Items) != 0 {
		t.Fatalf("retirement left tasks ready: %+v", ready.Items)
	}
	for _, taskID := range []string{"T-BASE", "T-CONSUMER"} {
		if _, err := store.LeaseTask(ctx, taskID, "agent", time.Minute, "tester"); err == nil {
			t.Fatalf("leased blocked task %s", taskID)
		}
	}
	if _, err := store.ConfirmRequirement(ctx, domain.RequirementRef{ID: base.ID}, "def456", "code_changed", "tester"); err == nil {
		t.Fatal("confirmed a retired requirement")
	}
}
