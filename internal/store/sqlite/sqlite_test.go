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

func TestComputedRequirementWorkStatus(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "reqdb.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	input := requirement("SWR-WORKABLE-001", "software", 1)
	item, err := store.CreateRequirement(ctx, input, "tester")
	if err != nil {
		t.Fatal(err)
	}
	item, err = store.GetRequirement(ctx, domain.RequirementRef{ID: input.ID})
	if err != nil {
		t.Fatal(err)
	}
	assertWorkability(t, item.Workability, false, "awaiting_review")
	if _, _, err := store.ReviewRequirement(ctx, domain.ReviewInput{Requirement: domain.RequirementRef{ID: input.ID}, Commit: strings.Repeat("d", 40), Verdict: "reject", Findings: []domain.ReviewFinding{{Message: "The implementation is absent."}}}, "reviewer"); err != nil {
		t.Fatal(err)
	}
	item, _ = store.GetRequirement(ctx, domain.RequirementRef{ID: input.ID})
	assertWorkability(t, item.Workability, false, "needs_task")

	taskInput := domain.TaskInput{Schema: "task/v1", ID: "T-700", Title: "Implement", Description: "Implement the requirement.", Priority: 50, Requirements: []domain.TaskRequirementInput{{Requirement: input.ID + "@1", Purpose: "implement"}}}
	task, err := store.CreateTask(ctx, taskInput, "tester")
	if err != nil {
		t.Fatal(err)
	}
	task, err = store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertWorkability(t, task.Workability, true, "ready_to_lease")
	item, _ = store.GetRequirement(ctx, domain.RequirementRef{ID: input.ID})
	assertWorkability(t, item.Workability, true, "ready_for_work")

	lease, err := store.LeaseTask(ctx, task.ID, "agent", time.Minute, "tester")
	if err != nil {
		t.Fatal(err)
	}
	task, _ = store.GetTask(ctx, task.ID)
	assertWorkability(t, task.Workability, false, "work_in_progress")
	item, _ = store.GetRequirement(ctx, domain.RequirementRef{ID: input.ID})
	assertWorkability(t, item.Workability, false, "work_in_progress")

	commit := strings.Repeat("e", 40)
	if _, err := store.CompleteTask(ctx, task.ID, lease.LeaseID, lease.Fence, commit, "tester"); err != nil {
		t.Fatal(err)
	}
	task, _ = store.GetTask(ctx, task.ID)
	assertWorkability(t, task.Workability, false, "complete")
	item, _ = store.GetRequirement(ctx, domain.RequirementRef{ID: input.ID})
	assertWorkability(t, item.Workability, false, "awaiting_review")
	if _, _, err := store.ReviewRequirement(ctx, domain.ReviewInput{Requirement: domain.RequirementRef{ID: input.ID}, Commit: commit, TaskID: task.ID, Verdict: "accept"}, "reviewer"); err != nil {
		t.Fatal(err)
	}
	item, _ = store.GetRequirement(ctx, domain.RequirementRef{ID: input.ID})
	assertWorkability(t, item.Workability, false, "no_work_required")
	if len(item.Workability.Reasons) == 0 || !strings.Contains(item.Workability.Reasons[0], "no new work is required") {
		t.Fatalf("satisfied requirement has unclear reasons: %+v", item.Workability)
	}
}

func assertWorkability(t *testing.T, actual *domain.Workability, workable bool, workStatus string) {
	t.Helper()
	if actual == nil || actual.Workable != workable || actual.WorkStatus != workStatus || len(actual.Reasons) == 0 {
		t.Fatalf("workability is %+v, expected workable=%t work_status=%s with reasons", actual, workable, workStatus)
	}
}

func TestReviewVerdictsFindingsAndIdempotency(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "reqdb.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	input := requirement("SWR-REVIEW-001", "software", 1)
	if _, err := store.CreateRequirement(ctx, input, "tester"); err != nil {
		t.Fatal(err)
	}
	commit := strings.Repeat("a", 40)
	rejected := domain.ReviewInput{
		Requirement: domain.RequirementRef{ID: input.ID}, Commit: commit, Verdict: "reject",
		Findings: []domain.ReviewFinding{{Message: "The code does not return the required result.", Path: "result.go", Line: 12}},
	}
	first, created, err := store.ReviewRequirement(ctx, rejected, "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if !created || first.ReconciliationState != domain.NotSatisfied || len(first.Reviews) != 1 || len(first.Reviews[0].Findings) != 1 {
		t.Fatalf("unexpected rejected review: %+v", first)
	}
	second, created, err := store.ReviewRequirement(ctx, rejected, "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if created || len(second.Reviews) != 1 || second.Reviews[0].ID != first.Reviews[0].ID {
		t.Fatalf("review was not idempotent: %+v", second.Reviews)
	}
	conflict := rejected
	conflict.Findings = []domain.ReviewFinding{{Message: "A different result."}}
	if _, _, err := store.ReviewRequirement(ctx, conflict, "reviewer"); err == nil || !strings.Contains(err.Error(), "different review") {
		t.Fatalf("unexpected conflicting review result: %v", err)
	}
	acceptedCommit := strings.Repeat("b", 40)
	accepted, _, err := store.ReviewRequirement(ctx, domain.ReviewInput{Requirement: domain.RequirementRef{ID: input.ID}, Commit: acceptedCommit, Verdict: "accept"}, "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if accepted.ReconciliationState != domain.Satisfied || len(accepted.Reviews) != 2 {
		t.Fatalf("unexpected accepted review: %+v", accepted)
	}
	if accepted.Reviews[0].Requirement != (domain.RequirementRef{ID: input.ID, Revision: 1}) {
		t.Fatalf("review does not identify its requirement: %+v", accepted.Reviews[0])
	}
	stored, err := store.GetReview(ctx, first.Reviews[0].ID)
	if err != nil || stored.Requirement.ID != input.ID || len(stored.Findings) != 1 {
		t.Fatalf("get review returned %+v, %v", stored, err)
	}
	page, err := store.ListReviews(ctx, domain.RequirementRef{ID: input.ID, Revision: 1}, "", 1)
	if err != nil || len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatalf("first review page returned %+v, %v", page, err)
	}
	next, err := store.ListReviews(ctx, domain.RequirementRef{ID: input.ID, Revision: 1}, page.NextCursor, 1)
	if err != nil || len(next.Items) != 1 || next.Items[0].ID == page.Items[0].ID {
		t.Fatalf("second review page returned %+v, %v", next, err)
	}
}

func TestReviewTaskMustMatchRevisionAndCommit(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "reqdb.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	first := requirement("SWR-REVIEW-TASK-001", "software", 1)
	second := requirement("SWR-REVIEW-TASK-002", "software", 1)
	for _, input := range []domain.RequirementInput{first, second} {
		if _, err := store.CreateRequirement(ctx, input, "tester"); err != nil {
			t.Fatal(err)
		}
	}
	task := domain.TaskInput{Schema: "task/v1", ID: "T-800", Title: "Implement", Description: "Implement the first requirement.", Priority: 50, Requirements: []domain.TaskRequirementInput{{Requirement: first.ID + "@1", Purpose: "implement"}}}
	if _, err := store.CreateTask(ctx, task, "tester"); err != nil {
		t.Fatal(err)
	}
	lease, err := store.LeaseTask(ctx, task.ID, "agent", time.Minute, "tester")
	if err != nil {
		t.Fatal(err)
	}
	commit := strings.Repeat("c", 40)
	if _, err := store.CompleteTask(ctx, task.ID, lease.LeaseID, lease.Fence, commit, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ReviewRequirement(ctx, domain.ReviewInput{Requirement: domain.RequirementRef{ID: second.ID}, Commit: commit, TaskID: task.ID, Verdict: "accept"}, "reviewer"); err == nil || !strings.Contains(err.Error(), "does not link") {
		t.Fatalf("unexpected task link result: %v", err)
	}
	if _, _, err := store.ReviewRequirement(ctx, domain.ReviewInput{Requirement: domain.RequirementRef{ID: first.ID}, Commit: strings.Repeat("d", 40), TaskID: task.ID, Verdict: "accept"}, "reviewer"); err == nil || !strings.Contains(err.Error(), "not complete at commit") {
		t.Fatalf("unexpected task commit result: %v", err)
	}
	item, _, err := store.ReviewRequirement(ctx, domain.ReviewInput{Requirement: domain.RequirementRef{ID: first.ID}, Commit: commit, TaskID: task.ID, Verdict: "accept"}, "reviewer")
	if err != nil || item.ReconciliationState != domain.Satisfied {
		t.Fatalf("matching task review failed: %+v, %v", item, err)
	}
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
	}
	if _, _, err := store.ReviewRequirement(ctx, domain.ReviewInput{Requirement: domain.RequirementRef{ID: "SWR-TEST-001"}, Commit: strings.Repeat("a", 40), Verdict: "accept"}, "tester"); err != nil {
		t.Fatalf("review software leaf: %v", err)
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
		expected := domain.PendingReview
		if item.ReconciliationState != expected {
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
	if item.ReconciliationState != domain.PendingReview {
		t.Fatalf("state is %s", item.ReconciliationState)
	}
	assertWorkability(t, item.Workability, false, "awaiting_review")
	if _, _, err := store.ReviewRequirement(ctx, domain.ReviewInput{Requirement: domain.RequirementRef{ID: "SWR-TEST-001"}, Commit: strings.Repeat("b", 40), Verdict: "accept"}, "tester"); err != nil {
		t.Fatal(err)
	}
}

func TestRequirementImplementationRollsUpFromActiveLeaves(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "reqdb.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	root := requirement("BR-ROLLUP-001", "business", 1)
	if _, err := store.CreateRequirement(ctx, root, "tester"); err != nil {
		t.Fatal(err)
	}
	child := requirement("STR-ROLLUP-001", "stakeholder", 1, "BR-ROLLUP-001@1")
	if _, err := store.CreateRequirement(ctx, child, "tester"); err != nil {
		t.Fatal(err)
	}
	grandchild := requirement("SYR-ROLLUP-001", "system", 1, "STR-ROLLUP-001@1")
	if _, err := store.CreateRequirement(ctx, grandchild, "tester"); err != nil {
		t.Fatal(err)
	}
	software := requirement("SWR-ROLLUP-001", "software", 1, "SYR-ROLLUP-001@1")
	if _, err := store.CreateRequirement(ctx, software, "tester"); err != nil {
		t.Fatal(err)
	}

	stored, err := store.GetRequirement(ctx, domain.RequirementRef{ID: root.ID})
	if err != nil {
		t.Fatal(err)
	}
	if stored.ReconciliationState != domain.PendingReview {
		t.Fatalf("parent state is %s", stored.ReconciliationState)
	}
	assertReadyRequirements(t, store)
	if _, _, err := store.ReviewRequirement(ctx, domain.ReviewInput{Requirement: domain.RequirementRef{ID: root.ID}, Commit: strings.Repeat("b", 40), Verdict: "accept"}, "tester"); err == nil || !strings.Contains(err.Error(), "not a software requirement") {
		t.Fatalf("unexpected parent review result: %v", err)
	}
	parentTask := domain.TaskInput{Schema: "task/v1", ID: "T-90", Title: "Implement parent", Description: "Implement the parent requirement.", Priority: 50, Requirements: []domain.TaskRequirementInput{{Requirement: "BR-ROLLUP-001@1", Purpose: "implement"}}}
	if _, err := store.CreateTask(ctx, parentTask, "tester"); err == nil || !strings.Contains(err.Error(), "not a software requirement") {
		t.Fatalf("unexpected parent task result: %v", err)
	}
	if _, _, err := store.ReviewRequirement(ctx, domain.ReviewInput{Requirement: domain.RequirementRef{ID: software.ID}, Commit: strings.Repeat("b", 40), Verdict: "accept"}, "tester"); err != nil {
		t.Fatal(err)
	}
	stored, err = store.GetRequirement(ctx, domain.RequirementRef{ID: root.ID})
	if err != nil {
		t.Fatal(err)
	}
	if stored.ReconciliationState != domain.Satisfied {
		t.Fatalf("satisfied child left parent %s", stored.ReconciliationState)
	}
	assertReadyRequirements(t, store)
	satisfied, err := store.ListRequirements(ctx, "", 20, "business", string(domain.Satisfied))
	if err != nil {
		t.Fatal(err)
	}
	if len(satisfied.Items) != 1 || satisfied.Items[0].ID != root.ID {
		t.Fatalf("satisfied roll-up filter returned %+v", satisfied.Items)
	}
	if _, err := store.RetireRequirement(ctx, child.ID, "tester"); err != nil {
		t.Fatal(err)
	}
	stored, err = store.GetRequirement(ctx, domain.RequirementRef{ID: root.ID})
	if err != nil {
		t.Fatal(err)
	}
	if stored.ReconciliationState != domain.PendingReview {
		t.Fatalf("retired child affected parent state: %s", stored.ReconciliationState)
	}
}

func TestNonSoftwareRequirementCannotHaveTask(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "reqdb.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	root := requirement("BR-TASK-ROLLUP-001", "business", 1)
	if _, err := store.CreateRequirement(ctx, root, "tester"); err != nil {
		t.Fatal(err)
	}
	task := domain.TaskInput{Schema: "task/v1", ID: "T-91", Title: "Implement root", Description: "Implement the root requirement.", Priority: 50, Requirements: []domain.TaskRequirementInput{{Requirement: "BR-TASK-ROLLUP-001@1", Purpose: "implement"}}}
	if _, err := store.CreateTask(ctx, task, "tester"); err == nil || !strings.Contains(err.Error(), "not a software requirement") {
		t.Fatalf("unexpected non-software task result: %v", err)
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
	if err := database.QueryRow(`SELECT count(*) FROM schema_migrations WHERE id IN ('SCHEMA_INIT', '202608180001', '202608180002', '202608180003', '202608190001', '202608210001', '202608220001')`).Scan(&migrations); err != nil {
		t.Fatal(err)
	}
	if migrations != 7 {
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

func TestReviewMigrationPreservesConfirmationAndRenamesState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reqdb.sqlite")
	legacy := strings.ReplaceAll(dbschema.Schema, "not_satisfied", "unimplemented")
	legacy = strings.ReplaceAll(legacy, "satisfied", "implemented")
	start := strings.Index(legacy, "CREATE TABLE requirement_review")
	end := strings.Index(legacy, "CREATE TABLE reconciliation_cause")
	legacyReview := `CREATE TABLE reconciliation_confirmation (
    id INTEGER PRIMARY KEY,
    requirement_id TEXT NOT NULL,
    requirement_revision INTEGER NOT NULL,
    result TEXT NOT NULL,
    commit_sha TEXT NOT NULL,
    task_id TEXT REFERENCES task(id),
    pull_request_id INTEGER REFERENCES pull_request(id),
    confirmed_at TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    note TEXT,
    FOREIGN KEY (requirement_id, requirement_revision)
        REFERENCES requirement_revision(requirement_id, revision)
);
`
	legacy = legacy[:start] + legacyReview + legacy[end:]
	database, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE schema_migrations (id TEXT PRIMARY KEY);
INSERT INTO schema_migrations VALUES ('SCHEMA_INIT'),('202608180001'),('202608180002'),('202608180003'),('202608190001');
BEGIN;
INSERT INTO requirement (id,current_revision,lifecycle_state,reconciliation_state,created_at,updated_at)
VALUES ('BR-MIGRATE-001',1,'active','implemented','2026-08-21T00:00:00Z','2026-08-21T00:00:00Z');
INSERT INTO requirement_revision (requirement_id,revision,level,title,statement,created_at,actor_id)
VALUES ('BR-MIGRATE-001',1,'business','Migrate','The organization shall migrate.','2026-08-21T00:00:00Z','tester');
INSERT INTO reconciliation_confirmation (requirement_id,requirement_revision,result,commit_sha,confirmed_at,actor_id)
VALUES ('BR-MIGRATE-001',1,'existing_code_confirmed','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','2026-08-21T00:00:00Z','tester');
INSERT INTO requirement (id,current_revision,lifecycle_state,reconciliation_state,created_at,updated_at)
VALUES ('BR-MIGRATE-PENDING',1,'active','unimplemented','2026-08-21T00:00:00Z','2026-08-21T00:00:00Z');
INSERT INTO requirement_revision (requirement_id,revision,level,title,statement,created_at,actor_id)
VALUES ('BR-MIGRATE-PENDING',1,'business','Pending','The organization shall wait.','2026-08-21T00:00:00Z','tester');
COMMIT;`); err != nil {
		t.Fatal(err)
	}
	_ = database.Close()

	store, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	item, err := store.GetRequirement(context.Background(), domain.RequirementRef{ID: "BR-MIGRATE-001"})
	if err != nil {
		t.Fatal(err)
	}
	if item.ReconciliationState != domain.Satisfied || len(item.Reviews) != 1 || item.Reviews[0].Verdict != "accept" || !strings.HasPrefix(item.Reviews[0].ID, "RV-MIGRATED-") {
		t.Fatalf("unexpected migrated requirement: %+v", item)
	}
	pending, err := store.GetRequirement(context.Background(), domain.RequirementRef{ID: "BR-MIGRATE-PENDING"})
	if err != nil || pending.ReconciliationState != domain.PendingReview {
		t.Fatalf("unexpected pending migration: %+v, %v", pending, err)
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

func TestRequirementDependenciesBlockTaskUntilSatisfied(t *testing.T) {
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
	if _, _, err := store.ReviewRequirement(ctx, domain.ReviewInput{Requirement: domain.RequirementRef{ID: prerequisite.ID}, Commit: strings.Repeat("a", 40), Verdict: "accept"}, "tester"); err != nil {
		t.Fatal(err)
	}
	ready, err = store.ListTasks(ctx, "", 20, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(ready.Items) != 1 || ready.Items[0].ID != task.ID {
		t.Fatalf("satisfied prerequisite did not unblock task: %+v", ready.Items)
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
		if _, _, err := store.ReviewRequirement(ctx, domain.ReviewInput{Requirement: domain.RequirementRef{ID: input.ID}, Commit: strings.Repeat("a", 40), Verdict: "accept"}, "tester"); err != nil {
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
	if stored.ReconciliationState != domain.PendingReview {
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
		if _, _, err := store.ReviewRequirement(ctx, domain.ReviewInput{Requirement: domain.RequirementRef{ID: id}, Commit: strings.Repeat("a", 40), Verdict: "accept"}, "tester"); err != nil {
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
		if _, _, err := store.ReviewRequirement(ctx, domain.ReviewInput{Requirement: domain.RequirementRef{ID: input.ID}, Commit: strings.Repeat("a", 40), Verdict: "accept"}, "tester"); err != nil {
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
	if stored.ReconciliationState != domain.PendingReview {
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
	if _, _, err := store.ReviewRequirement(ctx, domain.ReviewInput{Requirement: domain.RequirementRef{ID: base.ID}, Commit: strings.Repeat("b", 40), Verdict: "accept"}, "tester"); err == nil {
		t.Fatal("reviewed a retired requirement")
	}
}

func TestReadyRequirementsFollowDependenciesAndTasks(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "reqdb.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	base := requirement("SWR-BASE-001", "software", 1)
	dependent := requirement("SWR-DEPENDENT-001", "software", 1)
	dependent.Links.DependsOn = []string{"SWR-BASE-001@1"}
	for _, input := range []domain.RequirementInput{base, dependent} {
		if _, err := store.CreateRequirement(ctx, input, "tester"); err != nil {
			t.Fatal(err)
		}
	}
	assertReadyRequirements(t, store)

	task := domain.TaskInput{Schema: "task/v1", ID: "T-1", Title: "Implement base", Description: "Implement the base requirement.", Priority: 50, Requirements: []domain.TaskRequirementInput{{Requirement: "SWR-BASE-001@1", Purpose: "implement"}}}
	if _, err := store.CreateTask(ctx, task, "tester"); err != nil {
		t.Fatal(err)
	}
	assertReadyRequirements(t, store, "SWR-BASE-001")

	lease, err := store.LeaseTask(ctx, task.ID, "agent", time.Minute, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteTask(ctx, task.ID, lease.LeaseID, lease.Fence, "abc123", "tester"); err != nil {
		t.Fatal(err)
	}
	assertReadyRequirements(t, store)

	if _, _, err := store.ReviewRequirement(ctx, domain.ReviewInput{Requirement: domain.RequirementRef{ID: base.ID}, Commit: strings.Repeat("a", 40), Verdict: "accept"}, "tester"); err != nil {
		t.Fatal(err)
	}
	dependentTask := domain.TaskInput{Schema: "task/v1", ID: "T-2", Title: "Implement dependent", Description: "Implement the dependent requirement.", Priority: 50, Requirements: []domain.TaskRequirementInput{{Requirement: "SWR-DEPENDENT-001@1", Purpose: "implement"}}}
	if _, err := store.CreateTask(ctx, dependentTask, "tester"); err != nil {
		t.Fatal(err)
	}
	assertReadyRequirements(t, store, "SWR-DEPENDENT-001")
	if _, err := store.RetireRequirement(ctx, dependent.ID, "tester"); err != nil {
		t.Fatal(err)
	}
	assertReadyRequirements(t, store)
}

func assertReadyRequirements(t *testing.T, store *sqlite.Store, expected ...string) {
	t.Helper()
	page, err := store.ListWorkableRequirements(context.Background(), "", 20)
	if err != nil {
		t.Fatal(err)
	}
	actual := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		actual = append(actual, item.ID)
	}
	if strings.Join(actual, ",") != strings.Join(expected, ",") {
		t.Fatalf("ready requirements are %v, expected %v", actual, expected)
	}
}
