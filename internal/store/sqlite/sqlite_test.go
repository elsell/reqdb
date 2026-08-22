package sqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elsell/reqdb/internal/domain"
	"github.com/elsell/reqdb/internal/store/sqlite"
	_ "github.com/mattn/go-sqlite3"
)

func openStore(t *testing.T) (*sqlite.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "reqdb.sqlite")
	store, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, path
}

func requirement(id, level string, revision int, parents ...string) domain.RequirementInput {
	input := domain.RequirementInput{Schema: "requirement/v1", ID: id, Level: level, Revision: revision, Title: id, Statement: fmt.Sprintf("The %s shall provide one result.", level)}
	input.Links.Refines = parents
	return input
}

func create(t *testing.T, store *sqlite.Store, inputs ...domain.RequirementInput) {
	t.Helper()
	for _, input := range inputs {
		if _, err := store.CreateRequirement(context.Background(), input, "tester"); err != nil {
			t.Fatalf("create %s: %v", input.ID, err)
		}
	}
}

func task(id, requirement string) domain.TaskInput {
	return domain.TaskInput{Schema: "task/v1", ID: id, Title: "Implement", Description: "Implement the linked requirement.", Priority: 50, Requirements: []domain.TaskRequirementInput{{Requirement: requirement, Purpose: "implement"}}}
}

func review(t *testing.T, store *sqlite.Store, id, verdict, commit string) domain.Requirement {
	t.Helper()
	input := domain.ReviewInput{Requirement: domain.RequirementRef{ID: id}, Commit: commit, Verdict: verdict}
	if verdict == "reject" {
		input.Findings = []domain.ReviewFinding{{Message: "The implementation does not satisfy the requirement."}}
	}
	item, _, err := store.ReviewRequirement(context.Background(), input, "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func assertWorkStatus(t *testing.T, item domain.Requirement, workable bool, status string) {
	t.Helper()
	if item.Workability == nil || item.Workability.Workable != workable || item.Workability.WorkStatus != status || len(item.Workability.Reasons) == 0 {
		t.Fatalf("workability is %+v, expected workable=%t status=%s", item.Workability, workable, status)
	}
}

func TestStakeholderLeafCanHaveTasksAndReviews(t *testing.T) {
	store, _ := openStore(t)
	create(t, store,
		requirement("BR-LEAF-001", "business", 1),
		requirement("STR-LEAF-001", "stakeholder", 1, "BR-LEAF-001@1"),
	)
	if _, err := store.CreateTask(context.Background(), task("T-1", "STR-LEAF-001@1"), "tester"); err != nil {
		t.Fatal(err)
	}
	item := review(t, store, "STR-LEAF-001", "reject", strings.Repeat("a", 40))
	if item.ReconciliationState != domain.NotSatisfied {
		t.Fatalf("state is %s", item.ReconciliationState)
	}
}

func TestStakeholderWithSystemChildIsManagedThroughChildren(t *testing.T) {
	store, _ := openStore(t)
	create(t, store,
		requirement("BR-MANAGED-001", "business", 1),
		requirement("STR-MANAGED-001", "stakeholder", 1, "BR-MANAGED-001@1"),
		requirement("SYR-MANAGED-001", "system", 1, "STR-MANAGED-001@1"),
	)
	if _, err := store.CreateTask(context.Background(), task("T-1", "STR-MANAGED-001@1"), "tester"); err == nil || !strings.Contains(err.Error(), "not an actionable leaf") {
		t.Fatalf("unexpected task result: %v", err)
	}
	if _, _, err := store.ReviewRequirement(context.Background(), domain.ReviewInput{Requirement: domain.RequirementRef{ID: "STR-MANAGED-001"}, Commit: strings.Repeat("a", 40), Verdict: "accept"}, "reviewer"); err == nil || !strings.Contains(err.Error(), "not an actionable leaf") {
		t.Fatalf("unexpected review result: %v", err)
	}
	item, _ := store.GetRequirement(context.Background(), domain.RequirementRef{ID: "STR-MANAGED-001"})
	assertWorkStatus(t, item, false, "managed_through_children")
}

func TestSystemRequirementCanHaveTasksAndReviews(t *testing.T) {
	store, _ := openStore(t)
	create(t, store,
		requirement("BR-SYSTEM-001", "business", 1),
		requirement("STR-SYSTEM-001", "stakeholder", 1, "BR-SYSTEM-001@1"),
		requirement("SYR-SYSTEM-001", "system", 1, "STR-SYSTEM-001@1"),
	)
	if _, err := store.CreateTask(context.Background(), task("T-1", "SYR-SYSTEM-001@1"), "tester"); err != nil {
		t.Fatal(err)
	}
	item := review(t, store, "SYR-SYSTEM-001", "reject", strings.Repeat("a", 40))
	if item.ReconciliationState != domain.NotSatisfied {
		t.Fatalf("state is %s", item.ReconciliationState)
	}
}

func TestBusinessRequirementCannotHaveTasksOrReviews(t *testing.T) {
	store, _ := openStore(t)
	create(t, store, requirement("BR-MANAGED-001", "business", 1))
	if _, err := store.CreateTask(context.Background(), task("T-1", "BR-MANAGED-001@1"), "tester"); err == nil || !strings.Contains(err.Error(), "not an actionable leaf") {
		t.Fatalf("unexpected task result: %v", err)
	}
	if _, _, err := store.ReviewRequirement(context.Background(), domain.ReviewInput{Requirement: domain.RequirementRef{ID: "BR-MANAGED-001"}, Commit: strings.Repeat("a", 40), Verdict: "accept"}, "reviewer"); err == nil || !strings.Contains(err.Error(), "not an actionable leaf") {
		t.Fatalf("unexpected review result: %v", err)
	}
}

func TestFirstSystemChildRequiresCleanStakeholderRevision(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*sqlite.Store) error
	}{
		{name: "task", setup: func(store *sqlite.Store) error {
			_, err := store.CreateTask(context.Background(), task("T-1", "STR-DECOMPOSE-001@1"), "tester")
			return err
		}},
		{name: "review", setup: func(store *sqlite.Store) error {
			_, _, err := store.ReviewRequirement(context.Background(), domain.ReviewInput{Requirement: domain.RequirementRef{ID: "STR-DECOMPOSE-001"}, Commit: strings.Repeat("a", 40), Verdict: "accept"}, "reviewer")
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, _ := openStore(t)
			create(t, store,
				requirement("BR-DECOMPOSE-001", "business", 1),
				requirement("STR-DECOMPOSE-001", "stakeholder", 1, "BR-DECOMPOSE-001@1"),
			)
			if err := test.setup(store); err != nil {
				t.Fatal(err)
			}
			_, err := store.CreateRequirement(context.Background(), requirement("SYR-DECOMPOSE-001", "system", 1, "STR-DECOMPOSE-001@1"), "tester")
			if err == nil || !strings.Contains(err.Error(), "create a new revision before decomposition") {
				t.Fatalf("unexpected decomposition result: %v", err)
			}
		})
	}
}

func TestRetiringLastSystemChildMakesStakeholderActionable(t *testing.T) {
	store, _ := openStore(t)
	create(t, store,
		requirement("BR-RETIRE-001", "business", 1),
		requirement("STR-RETIRE-001", "stakeholder", 1, "BR-RETIRE-001@1"),
		requirement("SYR-RETIRE-001", "system", 1, "STR-RETIRE-001@1"),
	)
	if _, err := store.RetireRequirement(context.Background(), "SYR-RETIRE-001", "tester"); err != nil {
		t.Fatal(err)
	}
	item, err := store.GetRequirement(context.Background(), domain.RequirementRef{ID: "STR-RETIRE-001"})
	if err != nil {
		t.Fatal(err)
	}
	if item.ReconciliationState != domain.PendingReview {
		t.Fatalf("state is %s", item.ReconciliationState)
	}
	assertWorkStatus(t, item, false, "awaiting_review")
	if _, err := store.CreateTask(context.Background(), task("T-1", "STR-RETIRE-001@1"), "tester"); err != nil {
		t.Fatal(err)
	}
}

func TestCorrectiveTaskCompletionReturnsToPendingReview(t *testing.T) {
	store, _ := openStore(t)
	create(t, store,
		requirement("BR-CORRECT-001", "business", 1),
		requirement("STR-CORRECT-001", "stakeholder", 1, "BR-CORRECT-001@1"),
	)
	review(t, store, "STR-CORRECT-001", "reject", strings.Repeat("a", 40))
	created, err := store.CreateTask(context.Background(), task("T-1", "STR-CORRECT-001@1"), "tester")
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.LeaseTask(context.Background(), created.ID, "agent", time.Minute, "tester")
	if err != nil {
		t.Fatal(err)
	}
	item, _ := store.GetRequirement(context.Background(), domain.RequirementRef{ID: "STR-CORRECT-001"})
	if item.ReconciliationState != domain.NotSatisfied {
		t.Fatalf("lease changed reconciliation to %s", item.ReconciliationState)
	}
	if _, err := store.Heartbeat(context.Background(), lease.LeaseID, lease.Fence, time.Minute, "tester"); err != nil {
		t.Fatal(err)
	}
	item, _ = store.GetRequirement(context.Background(), domain.RequirementRef{ID: "STR-CORRECT-001"})
	if item.ReconciliationState != domain.NotSatisfied {
		t.Fatalf("heartbeat changed reconciliation to %s", item.ReconciliationState)
	}
	if _, err := store.CompleteTask(context.Background(), created.ID, lease.LeaseID, lease.Fence, strings.Repeat("b", 40), "tester"); err != nil {
		t.Fatal(err)
	}
	item, _ = store.GetRequirement(context.Background(), domain.RequirementRef{ID: "STR-CORRECT-001"})
	if item.ReconciliationState != domain.PendingReview {
		t.Fatalf("completion left reconciliation at %s", item.ReconciliationState)
	}
	assertWorkStatus(t, item, false, "awaiting_review")
}

func TestLeaseReleaseAndExpiryDoNotChangeReconciliation(t *testing.T) {
	store, _ := openStore(t)
	create(t, store,
		requirement("BR-LEASE-001", "business", 1),
		requirement("STR-LEASE-001", "stakeholder", 1, "BR-LEASE-001@1"),
	)
	review(t, store, "STR-LEASE-001", "reject", strings.Repeat("a", 40))
	created, _ := store.CreateTask(context.Background(), task("T-1", "STR-LEASE-001@1"), "tester")
	lease, _ := store.LeaseTask(context.Background(), created.ID, "agent", time.Minute, "tester")
	if err := store.Release(context.Background(), lease.LeaseID, lease.Fence, "tester"); err != nil {
		t.Fatal(err)
	}
	lease, _ = store.LeaseTask(context.Background(), created.ID, "agent", -time.Second, "tester")
	if err := store.ExpireLeases(context.Background(), "tester"); err != nil {
		t.Fatal(err)
	}
	item, _ := store.GetRequirement(context.Background(), domain.RequirementRef{ID: "STR-LEASE-001"})
	if item.ReconciliationState != domain.NotSatisfied {
		t.Fatalf("lease operation changed reconciliation to %s", item.ReconciliationState)
	}
}

func TestReconciliationRollsUpThroughOptionalSystemLevel(t *testing.T) {
	store, _ := openStore(t)
	create(t, store,
		requirement("BR-ROLLUP-001", "business", 1),
		requirement("STR-DIRECT-001", "stakeholder", 1, "BR-ROLLUP-001@1"),
		requirement("STR-SYSTEM-001", "stakeholder", 1, "BR-ROLLUP-001@1"),
		requirement("SYR-ROLLUP-001", "system", 1, "STR-SYSTEM-001@1"),
	)
	root, _ := store.GetRequirement(context.Background(), domain.RequirementRef{ID: "BR-ROLLUP-001"})
	if root.ReconciliationState != domain.PendingReview {
		t.Fatalf("initial roll-up is %s", root.ReconciliationState)
	}
	review(t, store, "STR-DIRECT-001", "accept", strings.Repeat("a", 40))
	review(t, store, "SYR-ROLLUP-001", "reject", strings.Repeat("b", 40))
	root, _ = store.GetRequirement(context.Background(), domain.RequirementRef{ID: "BR-ROLLUP-001"})
	if root.ReconciliationState != domain.NotSatisfied {
		t.Fatalf("rejected roll-up is %s", root.ReconciliationState)
	}
	review(t, store, "SYR-ROLLUP-001", "accept", strings.Repeat("c", 40))
	root, _ = store.GetRequirement(context.Background(), domain.RequirementRef{ID: "BR-ROLLUP-001"})
	if root.ReconciliationState != domain.Satisfied {
		t.Fatalf("accepted roll-up is %s", root.ReconciliationState)
	}
}

func TestAwaitingReviewAndNeedsTaskDeriveFromReconciliation(t *testing.T) {
	store, _ := openStore(t)
	create(t, store,
		requirement("BR-STATUS-001", "business", 1),
		requirement("STR-STATUS-001", "stakeholder", 1, "BR-STATUS-001@1"),
	)
	item, _ := store.GetRequirement(context.Background(), domain.RequirementRef{ID: "STR-STATUS-001"})
	assertWorkStatus(t, item, false, "awaiting_review")
	item = review(t, store, "STR-STATUS-001", "reject", strings.Repeat("a", 40))
	assertWorkStatus(t, item, false, "needs_task")
}

func TestRequirementDependencyBlocksTaskUntilExactRevisionIsSatisfied(t *testing.T) {
	store, _ := openStore(t)
	base := requirement("STR-BASE-001", "stakeholder", 1, "BR-DEPS-001@1")
	target := requirement("STR-TARGET-001", "stakeholder", 1, "BR-DEPS-001@1")
	target.Links.DependsOn = []string{"STR-BASE-001@1"}
	create(t, store, requirement("BR-DEPS-001", "business", 1), base, target)
	created, err := store.CreateTask(context.Background(), task("T-1", "STR-TARGET-001@1"), "tester")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LeaseTask(context.Background(), created.ID, "agent", time.Minute, "tester"); err == nil {
		t.Fatal("leased task with an unsatisfied requirement dependency")
	}
	review(t, store, "STR-BASE-001", "accept", strings.Repeat("a", 40))
	if _, err := store.LeaseTask(context.Background(), created.ID, "agent", time.Minute, "tester"); err != nil {
		t.Fatal(err)
	}
}

func TestReviewIsImmutableAndRejectRequiresFindings(t *testing.T) {
	store, _ := openStore(t)
	create(t, store,
		requirement("BR-REVIEW-001", "business", 1),
		requirement("STR-REVIEW-001", "stakeholder", 1, "BR-REVIEW-001@1"),
	)
	input := domain.ReviewInput{Requirement: domain.RequirementRef{ID: "STR-REVIEW-001"}, Commit: strings.Repeat("a", 40), Verdict: "reject", Findings: []domain.ReviewFinding{{Message: "One gap."}}}
	first, created, err := store.ReviewRequirement(context.Background(), input, "reviewer")
	if err != nil || !created || len(first.Reviews) != 1 {
		t.Fatalf("first review: %+v, %t, %v", first, created, err)
	}
	second, created, err := store.ReviewRequirement(context.Background(), input, "reviewer")
	if err != nil || created || second.Reviews[0].ID != first.Reviews[0].ID {
		t.Fatalf("idempotent review: %+v, %t, %v", second, created, err)
	}
}

func TestDatabaseReopensAndUsesOnlyCurrentSchema(t *testing.T) {
	store, path := openStore(t)
	create(t, store, requirement("BR-REOPEN-001", "business", 1))
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetRequirement(context.Background(), domain.RequirementRef{ID: "BR-REOPEN-001"}); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	database, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var legacyTables int
	if err := database.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='schema_migrations'`).Scan(&legacyTables); err != nil {
		t.Fatal(err)
	}
	if legacyTables != 0 {
		t.Fatal("new database contains migration metadata")
	}
	var schemaText string
	if err := database.QueryRow(`SELECT group_concat(sql, ' ') FROM sqlite_master WHERE sql IS NOT NULL`).Scan(&schemaText); err != nil {
		t.Fatal(err)
	}
	for _, obsolete := range []string{"software", "SWR-", "ready_for_review", "needs_reconciliation"} {
		if strings.Contains(schemaText, obsolete) {
			t.Fatalf("schema contains obsolete value %q", obsolete)
		}
	}
}
