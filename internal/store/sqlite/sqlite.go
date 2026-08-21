package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/elsell/reqdb/internal/domain"
	"github.com/elsell/reqdb/internal/ports"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var ErrNotFound = domain.ErrNotFound
var ErrConflict = domain.ErrConflict

type Store struct{ db *gorm.DB }

func Open(path string) (*Store, error) {
	db, err := gorm.Open(sqlite.Open(path+"?_foreign_keys=on&_busy_timeout=5000"), &gorm.Config{TranslateError: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	// SQLite PRAGMA settings are connection-local. Use one connection while
	// migrations temporarily change foreign-key enforcement.
	sqlDB.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	sqlDB.SetMaxOpenConns(0)
	return &Store{db: db}, nil
}

func (store *Store) Close() error {
	db, err := store.db.DB()
	if err != nil {
		return err
	}
	return db.Close()
}

type requirementRow struct {
	ID                  string `gorm:"column:id;primaryKey"`
	CurrentRevision     int
	LifecycleState      string
	ReconciliationState string
	CreatedAt           string
	UpdatedAt           string
}

func (requirementRow) TableName() string { return "requirement" }

type revisionRow struct {
	RequirementID string `gorm:"primaryKey"`
	Revision      int    `gorm:"primaryKey"`
	Level         string
	Title         string
	Statement     string
	CreatedAt     string
	ActorID       string
}

func (revisionRow) TableName() string { return "requirement_revision" }

type refinementRow struct {
	ChildID        string `gorm:"primaryKey"`
	ChildRevision  int    `gorm:"primaryKey"`
	ParentID       string `gorm:"primaryKey"`
	ParentRevision int    `gorm:"primaryKey"`
}

func (refinementRow) TableName() string { return "requirement_refinement" }

type requirementDependencyRow struct {
	RequirementID       string `gorm:"primaryKey"`
	RequirementRevision int    `gorm:"primaryKey"`
	DependencyID        string `gorm:"primaryKey"`
	DependencyRevision  int    `gorm:"primaryKey"`
}

func (requirementDependencyRow) TableName() string { return "requirement_dependency" }

type taskRow struct {
	ID                   string `gorm:"primaryKey"`
	Version              int
	Title, Description   string
	Priority             int
	State                string
	Fence                int
	CompletedCommit      *string
	CompletedAt          *string
	CreatedAt, UpdatedAt string
}

func (taskRow) TableName() string { return "task" }

type taskDependencyRow struct {
	TaskID       string `gorm:"primaryKey"`
	DependencyID string `gorm:"primaryKey"`
}

func (taskDependencyRow) TableName() string { return "task_dependency" }

type taskRequirementRow struct {
	TaskID              string `gorm:"primaryKey"`
	RequirementID       string `gorm:"primaryKey"`
	RequirementRevision int    `gorm:"primaryKey"`
	Purpose             string `gorm:"primaryKey"`
}

func (taskRequirementRow) TableName() string { return "task_requirement" }

type taskPullRequestRow struct {
	TaskID        string `gorm:"primaryKey"`
	PullRequestID int64  `gorm:"primaryKey"`
}

func (taskPullRequestRow) TableName() string { return "task_pull_request" }

type leaseRow struct {
	TaskID                            string `gorm:"primaryKey"`
	LeaseID, AgentID                  string
	Fence                             int
	ClaimedAt, HeartbeatAt, ExpiresAt string
}

func (leaseRow) TableName() string { return "lease" }

type stateHistoryRow struct {
	Sequence   int64 `gorm:"primaryKey"`
	EntityType string
	EntityID   string
	Field      string
	FromValue  *string
	ToValue    string
	OccurredAt string
	ActorID    string
}

func (stateHistoryRow) TableName() string { return "state_history" }

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func audit(ctx context.Context, tx *gorm.DB, actor, kind, entityType, entityID string, data any) error {
	value, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return tx.Exec(`INSERT INTO audit_event (occurred_at,actor_id,correlation_id,causation_id,kind,entity_type,entity_id,data_json) VALUES (?,?,?,?,?,?,?,?)`, now(), actor, ports.CorrelationID(ctx), ports.CausationID(ctx), kind, entityType, entityID, string(value)).Error
}

func recordState(tx *gorm.DB, entityType, entityID, field, from, to, actor string) error {
	if from == to {
		return nil
	}
	var previous *string
	if from != "" {
		previous = &from
	}
	return tx.Create(&stateHistoryRow{EntityType: entityType, EntityID: entityID, Field: field, FromValue: previous, ToValue: to, OccurredAt: now(), ActorID: actor}).Error
}

func setRequirementState(tx *gorm.DB, id string, state domain.ReconciliationState, actor string) error {
	var row requirementRow
	if err := tx.First(&row, "id=?", id).Error; err != nil {
		return err
	}
	if row.ReconciliationState == string(state) {
		return nil
	}
	if err := tx.Model(&requirementRow{}).Where("id=?", id).Updates(map[string]any{"reconciliation_state": string(state), "updated_at": now()}).Error; err != nil {
		return err
	}
	return recordState(tx, "requirement", id, "reconciliation", row.ReconciliationState, string(state), actor)
}

func setTaskState(tx *gorm.DB, id, state, actor string) error {
	var row taskRow
	if err := tx.First(&row, "id=?", id).Error; err != nil {
		return err
	}
	if row.State == state {
		return nil
	}
	if err := tx.Model(&taskRow{}).Where("id=?", id).Updates(map[string]any{"state": state, "updated_at": now()}).Error; err != nil {
		return err
	}
	return recordState(tx, "task", id, "state", row.State, state, actor)
}

func refs(input domain.RequirementInput) ([]domain.RequirementRef, error) {
	result := make([]domain.RequirementRef, 0, len(input.Links.Refines))
	for _, value := range input.Links.Refines {
		ref, err := domain.ParseRequirementRef(value)
		if err != nil {
			return nil, err
		}
		result = append(result, ref)
	}
	return result, nil
}

func dependencyRefs(input domain.RequirementInput) ([]domain.RequirementRef, error) {
	result := make([]domain.RequirementRef, 0, len(input.Links.DependsOn))
	for _, value := range input.Links.DependsOn {
		ref, err := domain.ParseRequirementRef(value)
		if err != nil {
			return nil, err
		}
		result = append(result, ref)
	}
	return result, nil
}

func addRevision(tx *gorm.DB, input domain.RequirementInput, actor string) error {
	parents, err := refs(input)
	if err != nil {
		return err
	}
	dependencies, err := dependencyRefs(input)
	if err != nil {
		return err
	}
	for _, parent := range parents {
		var count int64
		if err := tx.Model(&revisionRow{}).Where("requirement_id=? AND revision=?", parent.ID, parent.Revision).Count(&count).Error; err != nil || count != 1 {
			return fmt.Errorf("parent %s@%d: %w", parent.ID, parent.Revision, ErrNotFound)
		}
	}
	for _, dependency := range dependencies {
		var count int64
		if err := tx.Model(&revisionRow{}).Where("requirement_id=? AND revision=?", dependency.ID, dependency.Revision).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return friendlyNotFound{message: fmt.Sprintf("dependency requirement %s@%d does not exist", dependency.ID, dependency.Revision)}
		}
	}
	if err := tx.Create(&revisionRow{input.ID, input.Revision, input.Level, input.Title, input.Statement, now(), actor}).Error; err != nil {
		return err
	}
	for _, parent := range parents {
		if err := tx.Create(&refinementRow{input.ID, input.Revision, parent.ID, parent.Revision}).Error; err != nil {
			return err
		}
	}
	for _, dependency := range dependencies {
		if err := tx.Create(&requirementDependencyRow{input.ID, input.Revision, dependency.ID, dependency.Revision}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) CreateRequirement(ctx context.Context, input domain.RequirementInput, actor string) (domain.Requirement, error) {
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		stamp := now()
		if err := tx.Create(&requirementRow{ID: input.ID, CurrentRevision: input.Revision, LifecycleState: string(domain.Active), ReconciliationState: string(domain.NotSatisfied), CreatedAt: stamp, UpdatedAt: stamp}).Error; err != nil {
			return friendlyConflict{message: fmt.Sprintf("requirement %s already exists", input.ID)}
		}
		if err := addRevision(tx, input, actor); err != nil {
			return err
		}
		if err := recordState(tx, "requirement", input.ID, "lifecycle", "", string(domain.Active), actor); err != nil {
			return err
		}
		if err := recordState(tx, "requirement", input.ID, "reconciliation", "", string(domain.NotSatisfied), actor); err != nil {
			return err
		}
		return audit(ctx, tx, actor, "requirement.created", "requirement", input.ID, map[string]any{"revision": input.Revision})
	})
	if err != nil {
		return domain.Requirement{}, err
	}
	return store.GetRequirement(ctx, domain.RequirementRef{ID: input.ID})
}

type friendlyConflict struct{ message string }

func (err friendlyConflict) Error() string { return err.message }
func (err friendlyConflict) Unwrap() error { return ErrConflict }

type friendlyNotFound struct{ message string }

func (err friendlyNotFound) Error() string { return err.message }
func (err friendlyNotFound) Unwrap() error { return ErrNotFound }

func (store *Store) UpdateRequirement(ctx context.Context, input domain.RequirementInput, expected int, actor string) (domain.Requirement, error) {
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current requirementRow
		if err := tx.First(&current, "id = ?", input.ID).Error; err != nil {
			return ErrNotFound
		}
		if current.CurrentRevision != expected || input.Revision != expected+1 {
			return friendlyConflict{message: fmt.Sprintf("requirement %s is at revision %d, not revision %d", input.ID, current.CurrentRevision, expected)}
		}
		if current.LifecycleState == string(domain.Retired) {
			return friendlyConflict{message: fmt.Sprintf("requirement %s is retired", input.ID)}
		}
		if err := addRevision(tx, input, actor); err != nil {
			return err
		}
		previousState := current.ReconciliationState
		if err := tx.Model(&requirementRow{}).Where("id=? AND current_revision=?", input.ID, expected).Updates(map[string]any{"current_revision": input.Revision, "reconciliation_state": string(domain.NotSatisfied), "updated_at": now()}).Error; err != nil {
			return err
		}
		if err := recordState(tx, "requirement", input.ID, "reconciliation", previousState, string(domain.NotSatisfied), actor); err != nil {
			return err
		}
		type descendant struct {
			ID       string
			Revision int
		}
		var items []descendant
		query := `WITH RECURSIVE d(id) AS (
SELECT r.child_id FROM requirement_refinement r
JOIN requirement c ON c.id=r.child_id AND c.current_revision=r.child_revision
WHERE r.parent_id=?
UNION SELECT rd.requirement_id FROM requirement_dependency rd
JOIN requirement c ON c.id=rd.requirement_id AND c.current_revision=rd.requirement_revision
WHERE rd.dependency_id=?
UNION SELECT r.child_id FROM requirement_refinement r
JOIN requirement c ON c.id=r.child_id AND c.current_revision=r.child_revision
JOIN d ON r.parent_id=d.id
UNION SELECT rd.requirement_id FROM requirement_dependency rd
JOIN requirement c ON c.id=rd.requirement_id AND c.current_revision=rd.requirement_revision
JOIN d ON rd.dependency_id=d.id
) SELECT q.id, q.current_revision AS revision FROM requirement q JOIN d ON d.id=q.id WHERE q.lifecycle_state='active'`
		if err := tx.Raw(query, input.ID, input.ID).Scan(&items).Error; err != nil {
			return err
		}
		for _, item := range items {
			if err := setRequirementState(tx, item.ID, domain.NeedsReconciliation, actor); err != nil {
				return err
			}
			if err := tx.Exec(`INSERT OR IGNORE INTO reconciliation_cause (requirement_id,requirement_revision,cause_requirement_id,cause_revision,created_at) VALUES (?,?,?,?,?)`, item.ID, item.Revision, input.ID, input.Revision, now()).Error; err != nil {
				return err
			}
		}
		return audit(ctx, tx, actor, "requirement.revised", "requirement", input.ID, map[string]any{"revision": input.Revision, "affected": len(items)})
	})
	if err != nil {
		return domain.Requirement{}, err
	}
	return store.GetRequirement(ctx, domain.RequirementRef{ID: input.ID})
}

func (store *Store) GetRequirement(ctx context.Context, ref domain.RequirementRef) (domain.Requirement, error) {
	return store.getRequirement(ctx, ref, true)
}

func (store *Store) getRequirement(ctx context.Context, ref domain.RequirementRef, detail bool) (domain.Requirement, error) {
	var root requirementRow
	if err := store.db.WithContext(ctx).First(&root, "id=?", ref.ID).Error; err != nil {
		return domain.Requirement{}, ErrNotFound
	}
	if ref.Revision == 0 {
		ref.Revision = root.CurrentRevision
	}
	revision, err := store.loadRequirementRevision(ctx, ref)
	if err != nil {
		return domain.Requirement{}, ErrNotFound
	}
	effectiveState, err := effectiveRequirementState(store.db.WithContext(ctx), root.ID, root.CurrentRevision, domain.ReconciliationState(root.ReconciliationState))
	if err != nil {
		return domain.Requirement{}, err
	}
	item := domain.Requirement{ID: root.ID, CurrentRevision: root.CurrentRevision, LifecycleState: domain.LifecycleState(root.LifecycleState), ReconciliationState: effectiveState, Revision: revision}
	if !detail {
		return item, nil
	}
	var revisions []revisionRow
	if err := store.db.WithContext(ctx).Where("requirement_id=?", ref.ID).Order("revision").Find(&revisions).Error; err != nil {
		return domain.Requirement{}, err
	}
	item.RevisionHistory = make([]domain.RequirementRevision, 0, len(revisions))
	for _, row := range revisions {
		value, err := store.loadRequirementRevision(ctx, domain.RequirementRef{ID: ref.ID, Revision: row.Revision})
		if err != nil {
			return domain.Requirement{}, err
		}
		item.RevisionHistory = append(item.RevisionHistory, value)
	}
	item.StateHistory, err = store.stateHistory(ctx, "requirement", ref.ID)
	if err != nil {
		return domain.Requirement{}, err
	}
	hasChildren, err := hasActiveRefinementChildren(store.db.WithContext(ctx), root.ID, root.CurrentRevision)
	if err != nil {
		return domain.Requirement{}, err
	}
	if hasChildren {
		filtered := item.StateHistory[:0]
		for _, change := range item.StateHistory {
			if change.Field != "reconciliation" {
				filtered = append(filtered, change)
			}
		}
		item.StateHistory = filtered
	}
	item.Reviews, err = store.reviews(ctx, ref.ID)
	if err != nil {
		return domain.Requirement{}, err
	}
	item.OpenCauses, err = store.openCauses(ctx, ref.ID, root.CurrentRevision)
	if err != nil {
		return domain.Requirement{}, err
	}
	readiness, err := store.requirementReadiness(ctx, item)
	if err != nil {
		return domain.Requirement{}, err
	}
	item.Readiness = &readiness
	return item, nil
}

func hasActiveRefinementChildren(database *gorm.DB, id string, revision int) (bool, error) {
	var count int64
	err := database.Raw(`SELECT count(*) FROM requirement_refinement rr
JOIN requirement child ON child.id=rr.child_id
 AND child.current_revision=rr.child_revision
 AND child.lifecycle_state='active'
WHERE rr.parent_id=? AND rr.parent_revision=?`, id, revision).Scan(&count).Error
	return count > 0, err
}

func effectiveRequirementState(database *gorm.DB, id string, revision int, stored domain.ReconciliationState) (domain.ReconciliationState, error) {
	hasChildren, err := hasActiveRefinementChildren(database, id, revision)
	if err != nil || !hasChildren {
		return stored, err
	}
	var incompleteLeaves int64
	query := `WITH RECURSIVE descendants(id,revision,reconciliation_state) AS (
SELECT child.id,child.current_revision,child.reconciliation_state
FROM requirement_refinement rr
JOIN requirement child ON child.id=rr.child_id
 AND child.current_revision=rr.child_revision
 AND child.lifecycle_state='active'
WHERE rr.parent_id=? AND rr.parent_revision=?
UNION
SELECT child.id,child.current_revision,child.reconciliation_state
FROM requirement_refinement rr
JOIN requirement child ON child.id=rr.child_id
 AND child.current_revision=rr.child_revision
 AND child.lifecycle_state='active'
JOIN descendants parent ON rr.parent_id=parent.id AND rr.parent_revision=parent.revision
)
SELECT count(*) FROM descendants current
WHERE NOT EXISTS (
  SELECT 1 FROM requirement_refinement rr
  JOIN requirement child ON child.id=rr.child_id
   AND child.current_revision=rr.child_revision
   AND child.lifecycle_state='active'
  WHERE rr.parent_id=current.id AND rr.parent_revision=current.revision
)
AND current.reconciliation_state!='satisfied'`
	if err := database.Raw(query, id, revision).Scan(&incompleteLeaves).Error; err != nil {
		return "", err
	}
	if incompleteLeaves == 0 {
		return domain.Satisfied, nil
	}
	return domain.NotSatisfied, nil
}

func (store *Store) loadRequirementRevision(ctx context.Context, ref domain.RequirementRef) (domain.RequirementRevision, error) {
	var rev revisionRow
	if err := store.db.WithContext(ctx).First(&rev, "requirement_id=? AND revision=?", ref.ID, ref.Revision).Error; err != nil {
		return domain.RequirementRevision{}, err
	}
	var rows []refinementRow
	if err := store.db.WithContext(ctx).Where("child_id=? AND child_revision=?", ref.ID, ref.Revision).Find(&rows).Error; err != nil {
		return domain.RequirementRevision{}, err
	}
	parents := make([]domain.RequirementRef, 0, len(rows))
	for _, row := range rows {
		parents = append(parents, domain.RequirementRef{ID: row.ParentID, Revision: row.ParentRevision})
	}
	var dependencyRows []requirementDependencyRow
	if err := store.db.WithContext(ctx).Where("requirement_id=? AND requirement_revision=?", ref.ID, ref.Revision).Find(&dependencyRows).Error; err != nil {
		return domain.RequirementRevision{}, err
	}
	dependencies := make([]domain.RequirementRef, 0, len(dependencyRows))
	for _, row := range dependencyRows {
		dependencies = append(dependencies, domain.RequirementRef{ID: row.DependencyID, Revision: row.DependencyRevision})
	}
	created, _ := time.Parse(time.RFC3339Nano, rev.CreatedAt)
	return domain.RequirementRevision{RequirementID: rev.RequirementID, Revision: rev.Revision, Level: rev.Level, Title: rev.Title, Statement: rev.Statement, Parents: parents, Dependencies: dependencies, CreatedAt: created, ActorID: rev.ActorID}, nil
}

func (store *Store) ListRequirements(ctx context.Context, cursor string, limit int, level, state string) (domain.Page[domain.Requirement], error) {
	query := store.db.WithContext(ctx).Model(&requirementRow{}).Where("id > ?", cursor).Order("id")
	if level != "" {
		query = query.Joins("JOIN requirement_revision rr ON rr.requirement_id=requirement.id AND rr.revision=requirement.current_revision").Where("rr.level=?", level)
	}
	var rows []requirementRow
	if err := query.Find(&rows).Error; err != nil {
		return domain.Page[domain.Requirement]{}, err
	}
	items := make([]domain.Requirement, 0, limit+1)
	for _, row := range rows {
		item, err := store.getRequirement(ctx, domain.RequirementRef{ID: row.ID}, false)
		if err != nil {
			return domain.Page[domain.Requirement]{}, err
		}
		if state == "" || string(item.ReconciliationState) == state {
			items = append(items, item)
			if len(items) == limit+1 {
				break
			}
		}
	}
	page := domain.Page[domain.Requirement]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.NextCursor = items[limit-1].ID
	}
	return page, nil
}

func (store *Store) ListReadyRequirements(ctx context.Context, cursor string, limit int) (domain.Page[domain.Requirement], error) {
	query := store.db.WithContext(ctx).Model(&requirementRow{}).
		Where("requirement.id > ?", cursor).
		Where("requirement.lifecycle_state=?", domain.Active).
		Order("requirement.id")
	var rows []requirementRow
	if err := query.Find(&rows).Error; err != nil {
		return domain.Page[domain.Requirement]{}, err
	}
	items := make([]domain.Requirement, 0, limit+1)
	for _, row := range rows {
		item, err := store.getRequirement(ctx, domain.RequirementRef{ID: row.ID}, false)
		if err != nil {
			return domain.Page[domain.Requirement]{}, err
		}
		readiness, err := store.requirementReadiness(ctx, item)
		if err != nil {
			return domain.Page[domain.Requirement]{}, err
		}
		if readiness.Ready {
			items = append(items, item)
			if len(items) == limit+1 {
				break
			}
		}
	}
	page := domain.Page[domain.Requirement]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.NextCursor = items[limit-1].ID
	}
	return page, nil
}

func (store *Store) stateHistory(ctx context.Context, entityType, entityID string) ([]domain.StateChange, error) {
	var rows []stateHistoryRow
	if err := store.db.WithContext(ctx).Where("entity_type=? AND entity_id=?", entityType, entityID).Order("sequence").Find(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]domain.StateChange, 0, len(rows))
	for _, row := range rows {
		occurred, _ := time.Parse(time.RFC3339Nano, row.OccurredAt)
		from := ""
		if row.FromValue != nil {
			from = *row.FromValue
		}
		items = append(items, domain.StateChange{Sequence: row.Sequence, Field: row.Field, From: from, To: row.ToValue, OccurredAt: occurred, ActorID: row.ActorID})
	}
	return items, nil
}

func (store *Store) reviews(ctx context.Context, requirementID string) ([]domain.Review, error) {
	type row struct {
		ID, Verdict, CommitSHA, TaskID, ReviewedAt, ReviewerID string
		RequirementRevision                                    int
	}
	var rows []row
	query := `SELECT id,requirement_revision,verdict,commit_sha,COALESCE(task_id,'') AS task_id,reviewed_at,reviewer_id
FROM requirement_review WHERE requirement_id=? ORDER BY reviewed_at,id`
	if err := store.db.WithContext(ctx).Raw(query, requirementID).Scan(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]domain.Review, 0, len(rows))
	for _, row := range rows {
		reviewed, _ := time.Parse(time.RFC3339Nano, row.ReviewedAt)
		item := domain.Review{ID: row.ID, Requirement: domain.RequirementRef{ID: requirementID, Revision: row.RequirementRevision}, Verdict: row.Verdict, Commit: row.CommitSHA, TaskID: row.TaskID, ReviewedAt: reviewed, ReviewerID: row.ReviewerID}
		if err := store.db.WithContext(ctx).Raw(`SELECT message,path,line FROM review_finding WHERE review_id=? ORDER BY ordinal`, row.ID).Scan(&item.Findings).Error; err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (store *Store) GetReview(ctx context.Context, id string) (domain.Review, error) {
	type row struct {
		ID, RequirementID, Verdict, CommitSHA, TaskID, ReviewedAt, ReviewerID string
		RequirementRevision                                                   int
	}
	var value row
	if err := store.db.WithContext(ctx).Raw(`SELECT id,requirement_id,requirement_revision,verdict,commit_sha,COALESCE(task_id,'') AS task_id,reviewed_at,reviewer_id FROM requirement_review WHERE id=?`, id).Scan(&value).Error; err != nil {
		return domain.Review{}, err
	}
	if value.ID == "" {
		return domain.Review{}, ErrNotFound
	}
	reviewed, _ := time.Parse(time.RFC3339Nano, value.ReviewedAt)
	item := domain.Review{ID: value.ID, Requirement: domain.RequirementRef{ID: value.RequirementID, Revision: value.RequirementRevision}, Verdict: value.Verdict, Commit: value.CommitSHA, TaskID: value.TaskID, ReviewedAt: reviewed, ReviewerID: value.ReviewerID}
	if err := store.db.WithContext(ctx).Raw(`SELECT message,path,line FROM review_finding WHERE review_id=? ORDER BY ordinal`, id).Scan(&item.Findings).Error; err != nil {
		return domain.Review{}, err
	}
	return item, nil
}

func (store *Store) ListReviews(ctx context.Context, requirement domain.RequirementRef, cursor string, limit int) (domain.Page[domain.Review], error) {
	query := store.db.WithContext(ctx).Table("requirement_review").Where("requirement_id=?", requirement.ID)
	if requirement.Revision > 0 {
		query = query.Where("requirement_revision=?", requirement.Revision)
	}
	if cursor != "" {
		var cursorRow struct{ ReviewedAt string }
		cursorQuery := store.db.WithContext(ctx).Table("requirement_review").Select("reviewed_at").Where("id=? AND requirement_id=?", cursor, requirement.ID)
		if requirement.Revision > 0 {
			cursorQuery = cursorQuery.Where("requirement_revision=?", requirement.Revision)
		}
		if err := cursorQuery.Scan(&cursorRow).Error; err != nil {
			return domain.Page[domain.Review]{}, err
		}
		if cursorRow.ReviewedAt == "" {
			return domain.Page[domain.Review]{}, ErrNotFound
		}
		query = query.Where("reviewed_at>? OR (reviewed_at=? AND id>?)", cursorRow.ReviewedAt, cursorRow.ReviewedAt, cursor)
	}
	type row struct{ ID string }
	var rows []row
	if err := query.Select("id").Order("reviewed_at,id").Limit(limit + 1).Scan(&rows).Error; err != nil {
		return domain.Page[domain.Review]{}, err
	}
	page := domain.Page[domain.Review]{Items: []domain.Review{}}
	if len(rows) > limit {
		page.NextCursor = rows[limit-1].ID
		rows = rows[:limit]
	}
	for _, row := range rows {
		item, err := store.GetReview(ctx, row.ID)
		if err != nil {
			return domain.Page[domain.Review]{}, err
		}
		page.Items = append(page.Items, item)
	}
	return page, nil
}

func (store *Store) openCauses(ctx context.Context, requirementID string, revision int) ([]domain.ReconciliationCause, error) {
	type row struct {
		CauseRequirementID string
		CauseRevision      int
		CreatedAt          string
	}
	var rows []row
	if err := store.db.WithContext(ctx).Table("reconciliation_cause").Where("requirement_id=? AND requirement_revision=? AND resolved_at IS NULL", requirementID, revision).Order("created_at,cause_requirement_id").Find(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]domain.ReconciliationCause, 0, len(rows))
	for _, row := range rows {
		created, _ := time.Parse(time.RFC3339Nano, row.CreatedAt)
		items = append(items, domain.ReconciliationCause{Requirement: domain.RequirementRef{ID: row.CauseRequirementID, Revision: row.CauseRevision}, CreatedAt: created})
	}
	return items, nil
}

func (store *Store) requirementReadiness(ctx context.Context, item domain.Requirement) (domain.Readiness, error) {
	blockers := []string{}
	if item.Revision.Revision != item.CurrentRevision {
		blockers = append(blockers, fmt.Sprintf("revision %d is not current revision %d", item.Revision.Revision, item.CurrentRevision))
	}
	if item.LifecycleState != domain.Active {
		blockers = append(blockers, "requirement is retired")
	}
	hasChildren, err := hasActiveRefinementChildren(store.db.WithContext(ctx), item.ID, item.CurrentRevision)
	if err != nil {
		return domain.Readiness{}, err
	}
	if hasChildren {
		blockers = append(blockers, "requirement is not a leaf")
	}
	if item.ReconciliationState != domain.NotSatisfied && item.ReconciliationState != domain.NeedsReconciliation {
		blockers = append(blockers, fmt.Sprintf("reconciliation state is %s", item.ReconciliationState))
	}
	var taskIDs []string
	if err := store.db.WithContext(ctx).Raw(`SELECT t.id FROM task t JOIN task_requirement tr ON tr.task_id=t.id WHERE tr.requirement_id=? AND tr.requirement_revision=? AND t.state='open' ORDER BY t.id`, item.ID, item.CurrentRevision).Scan(&taskIDs).Error; err != nil {
		return domain.Readiness{}, err
	}
	for _, id := range taskIDs {
		blockers = append(blockers, fmt.Sprintf("open task %s already links to the current revision", id))
	}
	type dependency struct {
		ID, LifecycleState, ReconciliationState string
		Revision, CurrentRevision               int
	}
	var dependencies []dependency
	query := `WITH RECURSIVE deps(id,revision) AS (
SELECT dependency_id,dependency_revision FROM requirement_dependency WHERE requirement_id=? AND requirement_revision=?
UNION SELECT rd.dependency_id,rd.dependency_revision FROM requirement_dependency rd JOIN deps d ON rd.requirement_id=d.id AND rd.requirement_revision=d.revision
) SELECT d.id,d.revision,r.current_revision,r.lifecycle_state,r.reconciliation_state FROM deps d LEFT JOIN requirement r ON r.id=d.id ORDER BY d.id,d.revision`
	if err := store.db.WithContext(ctx).Raw(query, item.ID, item.CurrentRevision).Scan(&dependencies).Error; err != nil {
		return domain.Readiness{}, err
	}
	for _, dependency := range dependencies {
		ref := fmt.Sprintf("%s@%d", dependency.ID, dependency.Revision)
		effectiveState := domain.ReconciliationState(dependency.ReconciliationState)
		if dependency.CurrentRevision != 0 {
			effectiveState, err = effectiveRequirementState(store.db.WithContext(ctx), dependency.ID, dependency.CurrentRevision, effectiveState)
			if err != nil {
				return domain.Readiness{}, err
			}
		}
		switch {
		case dependency.CurrentRevision == 0:
			blockers = append(blockers, ref+" does not exist")
		case dependency.CurrentRevision != dependency.Revision:
			blockers = append(blockers, fmt.Sprintf("%s is stale; current revision is %d", ref, dependency.CurrentRevision))
		case dependency.LifecycleState != string(domain.Active):
			blockers = append(blockers, ref+" is retired")
		case effectiveState != domain.Satisfied:
			blockers = append(blockers, fmt.Sprintf("%s is %s", ref, effectiveState))
		}
	}
	return domain.Readiness{Ready: len(blockers) == 0, Blockers: blockers}, nil
}

func (store *Store) ReviewRequirement(ctx context.Context, input domain.ReviewInput, actor string) (domain.Requirement, bool, error) {
	ref := input.Requirement
	created := false
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var root requirementRow
		if err := tx.First(&root, "id=?", ref.ID).Error; err != nil {
			return ErrNotFound
		}
		if ref.Revision == 0 {
			ref.Revision = root.CurrentRevision
		}
		if ref.Revision != root.CurrentRevision {
			return ErrConflict
		}
		if root.LifecycleState == string(domain.Retired) {
			return friendlyConflict{message: fmt.Sprintf("requirement %s is retired", ref.ID)}
		}
		hasChildren, err := hasActiveRefinementChildren(tx, ref.ID, ref.Revision)
		if err != nil {
			return err
		}
		if hasChildren {
			return friendlyConflict{message: fmt.Sprintf("requirement %s@%d is not a leaf; review its refinement children", ref.ID, ref.Revision)}
		}

		type priorReview struct{ ID, Verdict, TaskID string }
		var prior priorReview
		if err := tx.Raw(`SELECT id,verdict,COALESCE(task_id,'') AS task_id FROM requirement_review WHERE requirement_id=? AND requirement_revision=? AND commit_sha=?`, ref.ID, ref.Revision, input.Commit).Scan(&prior).Error; err != nil {
			return err
		}
		if prior.ID != "" {
			var findings []domain.ReviewFinding
			if err := tx.Raw(`SELECT message,path,line FROM review_finding WHERE review_id=? ORDER BY ordinal`, prior.ID).Scan(&findings).Error; err != nil {
				return err
			}
			if prior.Verdict == input.Verdict && prior.TaskID == input.TaskID && reviewFindingsEqual(findings, input.Findings) {
				return nil
			}
			return friendlyConflict{message: fmt.Sprintf("commit %s already has a different review for %s@%d", input.Commit, ref.ID, ref.Revision)}
		}
		if root.ReconciliationState == string(domain.InProgress) || root.ReconciliationState == string(domain.Satisfied) {
			return friendlyConflict{message: fmt.Sprintf("requirement %s@%d cannot be reviewed while %s", ref.ID, ref.Revision, root.ReconciliationState)}
		}

		var taskID any
		if input.TaskID != "" {
			var task taskRow
			if err := tx.First(&task, "id=?", input.TaskID).Error; err != nil {
				return friendlyNotFound{message: fmt.Sprintf("task %s does not exist", input.TaskID)}
			}
			if task.State != "complete" || task.CompletedCommit == nil || *task.CompletedCommit != input.Commit {
				return friendlyConflict{message: fmt.Sprintf("task %s is not complete at commit %s", input.TaskID, input.Commit)}
			}
			var links int64
			if err := tx.Model(&taskRequirementRow{}).Where("task_id=? AND requirement_id=? AND requirement_revision=?", input.TaskID, ref.ID, ref.Revision).Count(&links).Error; err != nil {
				return err
			}
			if links == 0 {
				return friendlyConflict{message: fmt.Sprintf("task %s does not link to %s@%d", input.TaskID, ref.ID, ref.Revision)}
			}
			taskID = input.TaskID
		}

		reviewID := fmt.Sprintf("RV-%d", time.Now().UTC().UnixNano())
		stamp := now()
		if err := tx.Exec(`INSERT INTO requirement_review (id,requirement_id,requirement_revision,commit_sha,task_id,verdict,reviewed_at,reviewer_id) VALUES (?,?,?,?,?,?,?,?)`, reviewID, ref.ID, ref.Revision, input.Commit, taskID, input.Verdict, stamp, actor).Error; err != nil {
			return err
		}
		created = true
		for ordinal, finding := range input.Findings {
			if err := tx.Exec(`INSERT INTO review_finding (review_id,ordinal,message,path,line) VALUES (?,?,?,?,?)`, reviewID, ordinal, finding.Message, finding.Path, finding.Line).Error; err != nil {
				return err
			}
		}

		state := domain.NotSatisfied
		if input.Verdict == "accept" {
			if err := tx.Exec(`UPDATE reconciliation_cause SET resolved_at=? WHERE requirement_id=? AND requirement_revision=? AND resolved_at IS NULL`, stamp, ref.ID, ref.Revision).Error; err != nil {
				return err
			}
			state = domain.Satisfied
		} else {
			var causes int64
			if err := tx.Table("reconciliation_cause").Where("requirement_id=? AND requirement_revision=? AND resolved_at IS NULL", ref.ID, ref.Revision).Count(&causes).Error; err != nil {
				return err
			}
			if causes > 0 {
				state = domain.NeedsReconciliation
			}
		}
		if err := setRequirementState(tx, ref.ID, state, actor); err != nil {
			return err
		}
		return audit(ctx, tx, actor, "requirement.reviewed", "requirement", ref.ID, map[string]any{"review": reviewID, "revision": ref.Revision, "commit": input.Commit, "verdict": input.Verdict, "task": input.TaskID})
	})
	if err != nil {
		return domain.Requirement{}, false, err
	}
	item, err := store.GetRequirement(ctx, ref)
	return item, created, err
}

func reviewFindingsEqual(left, right []domain.ReviewFinding) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (store *Store) RetireRequirement(ctx context.Context, id, actor string) (domain.Requirement, error) {
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var root requirementRow
		if err := tx.First(&root, "id=?", id).Error; err != nil {
			return ErrNotFound
		}
		if root.LifecycleState == string(domain.Retired) {
			return friendlyConflict{message: fmt.Sprintf("requirement %s is already retired", id)}
		}
		if err := tx.Model(&requirementRow{}).Where("id=?", id).Updates(map[string]any{"lifecycle_state": string(domain.Retired), "updated_at": now()}).Error; err != nil {
			return err
		}
		if err := recordState(tx, "requirement", id, "lifecycle", root.LifecycleState, string(domain.Retired), actor); err != nil {
			return err
		}
		type downstream struct {
			ID       string
			Revision int
		}
		var items []downstream
		query := `WITH RECURSIVE d(id) AS (
SELECT r.child_id FROM requirement_refinement r
JOIN requirement c ON c.id=r.child_id AND c.current_revision=r.child_revision
WHERE r.parent_id=?
UNION SELECT rd.requirement_id FROM requirement_dependency rd
JOIN requirement c ON c.id=rd.requirement_id AND c.current_revision=rd.requirement_revision
WHERE rd.dependency_id=?
UNION SELECT r.child_id FROM requirement_refinement r
JOIN requirement c ON c.id=r.child_id AND c.current_revision=r.child_revision
JOIN d ON r.parent_id=d.id
UNION SELECT rd.requirement_id FROM requirement_dependency rd
JOIN requirement c ON c.id=rd.requirement_id AND c.current_revision=rd.requirement_revision
JOIN d ON rd.dependency_id=d.id
) SELECT q.id, q.current_revision AS revision FROM requirement q JOIN d ON d.id=q.id WHERE q.lifecycle_state='active'`
		if err := tx.Raw(query, id, id).Scan(&items).Error; err != nil {
			return err
		}
		for _, item := range items {
			if err := setRequirementState(tx, item.ID, domain.NeedsReconciliation, actor); err != nil {
				return err
			}
			if err := tx.Exec(`INSERT OR IGNORE INTO reconciliation_cause (requirement_id,requirement_revision,cause_requirement_id,cause_revision,created_at) VALUES (?,?,?,?,?)`, item.ID, item.Revision, id, root.CurrentRevision, now()).Error; err != nil {
				return err
			}
		}
		return audit(ctx, tx, actor, "requirement.retired", "requirement", id, map[string]any{"revision": root.CurrentRevision, "affected": len(items)})
	})
	if err != nil {
		return domain.Requirement{}, err
	}
	return store.GetRequirement(ctx, domain.RequirementRef{ID: id})
}

func (store *Store) Trace(ctx context.Context, root string) ([]domain.Requirement, error) {
	return store.graph(ctx, root, false)
}
func (store *Store) Impact(ctx context.Context, root string) ([]domain.Requirement, error) {
	return store.graph(ctx, root, true)
}
func (store *Store) graph(ctx context.Context, root string, dependencies bool) ([]domain.Requirement, error) {
	var ids []string
	if root == "" {
		if err := store.db.WithContext(ctx).Model(&requirementRow{}).Order("id").Pluck("id", &ids).Error; err != nil {
			return nil, err
		}
	} else {
		q := `WITH RECURSIVE d(id) AS (
SELECT ?
UNION SELECT r.child_id FROM requirement_refinement r
JOIN requirement c ON c.id=r.child_id AND c.current_revision=r.child_revision
JOIN d ON r.parent_id=d.id
) SELECT DISTINCT id FROM d ORDER BY id`
		if dependencies {
			q = `WITH RECURSIVE d(id) AS (
SELECT ?
UNION SELECT r.child_id FROM requirement_refinement r
JOIN requirement c ON c.id=r.child_id AND c.current_revision=r.child_revision
JOIN d ON r.parent_id=d.id
UNION SELECT rd.requirement_id FROM requirement_dependency rd
JOIN requirement c ON c.id=rd.requirement_id AND c.current_revision=rd.requirement_revision
JOIN d ON rd.dependency_id=d.id
) SELECT DISTINCT id FROM d ORDER BY id`
		}
		if err := store.db.WithContext(ctx).Raw(q, root).Scan(&ids).Error; err != nil {
			return nil, err
		}
	}
	items := make([]domain.Requirement, 0, len(ids))
	for _, id := range ids {
		item, err := store.getRequirement(ctx, domain.RequirementRef{ID: id}, false)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	order := map[string]int{"business": 0, "stakeholder": 1, "system": 2, "software": 3}
	sort.Slice(items, func(i, j int) bool {
		left, right := order[items[i].Revision.Level], order[items[j].Revision.Level]
		if left == right {
			return items[i].ID < items[j].ID
		}
		return left < right
	})
	return items, nil
}

func (store *Store) TasksForRequirements(ctx context.Context, refs []domain.RequirementRef) ([]domain.Task, error) {
	ids := make(map[string]bool)
	for _, ref := range refs {
		var taskIDs []string
		if err := store.db.WithContext(ctx).Model(&taskRequirementRow{}).Where("requirement_id=? AND requirement_revision=?", ref.ID, ref.Revision).Pluck("task_id", &taskIDs).Error; err != nil {
			return nil, err
		}
		for _, id := range taskIDs {
			ids[id] = true
		}
	}
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	tasks := make([]domain.Task, 0, len(ordered))
	for _, id := range ordered {
		task, err := store.getTask(ctx, id, false)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func (store *Store) CreateTask(ctx context.Context, input domain.TaskInput, actor string) (domain.Task, error) {
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		stamp := now()
		if err := tx.Create(&taskRow{ID: input.ID, Version: 1, Title: input.Title, Description: input.Description, Priority: input.Priority, State: "open", CreatedAt: stamp, UpdatedAt: stamp}).Error; err != nil {
			return friendlyConflict{message: fmt.Sprintf("task %s already exists", input.ID)}
		}
		if err := recordState(tx, "task", input.ID, "state", "", "open", actor); err != nil {
			return err
		}
		for _, dep := range input.DependsOn {
			var n int64
			if err := tx.Model(&taskRow{}).Where("id=?", dep).Count(&n).Error; err != nil || n == 0 {
				return fmt.Errorf("dependency %s: %w", dep, ErrNotFound)
			}
			if err := tx.Create(&taskDependencyRow{input.ID, dep}).Error; err != nil {
				return err
			}
		}
		for _, link := range input.Requirements {
			ref, err := domain.ParseRequirementRef(link.Requirement)
			if err != nil {
				return err
			}
			var count int64
			if err := tx.Model(&revisionRow{}).Where("requirement_id=? AND revision=?", ref.ID, ref.Revision).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				return friendlyNotFound{message: fmt.Sprintf("requirement %s@%d does not exist", ref.ID, ref.Revision)}
			}
			hasChildren, err := hasActiveRefinementChildren(tx, ref.ID, ref.Revision)
			if err != nil {
				return err
			}
			if hasChildren {
				return friendlyConflict{message: fmt.Sprintf("requirement %s@%d is not a leaf; link work to its refinement children", ref.ID, ref.Revision)}
			}
			if err := tx.Create(&taskRequirementRow{input.ID, ref.ID, ref.Revision, link.Purpose}).Error; err != nil {
				return err
			}
		}
		return audit(ctx, tx, actor, "task.created", "task", input.ID, nil)
	})
	if err != nil {
		return domain.Task{}, err
	}
	return store.GetTask(ctx, input.ID)
}

func (store *Store) GetTask(ctx context.Context, id string) (domain.Task, error) {
	return store.getTask(ctx, id, true)
}

func (store *Store) getTask(ctx context.Context, id string, detail bool) (domain.Task, error) {
	var row taskRow
	if err := store.db.WithContext(ctx).First(&row, "id=?", id).Error; err != nil {
		return domain.Task{}, ErrNotFound
	}
	var deps []string
	store.db.WithContext(ctx).Model(&taskDependencyRow{}).Where("task_id=?", id).Pluck("dependency_id", &deps)
	var links []taskRequirementRow
	store.db.WithContext(ctx).Where("task_id=?", id).Find(&links)
	reqs := make([]domain.TaskRequirementInput, 0, len(links))
	for _, l := range links {
		reqs = append(reqs, domain.TaskRequirementInput{Requirement: fmt.Sprintf("%s@%d", l.RequirementID, l.RequirementRevision), Purpose: l.Purpose})
	}
	commit := ""
	if row.CompletedCommit != nil {
		commit = *row.CompletedCommit
	}
	item := domain.Task{ID: row.ID, Version: row.Version, Title: row.Title, Description: row.Description, Priority: row.Priority, State: row.State, Fence: row.Fence, CompletedCommit: commit, Requirements: reqs, DependsOn: deps}
	type pullRequestRow struct {
		Repository string
		Number     int
		URL        string
	}
	var pullRequests []pullRequestRow
	query := `SELECT p.repository,p.number,p.url FROM pull_request p JOIN task_pull_request tpr ON tpr.pull_request_id=p.id WHERE tpr.task_id=? ORDER BY p.repository,p.number`
	if err := store.db.WithContext(ctx).Raw(query, id).Scan(&pullRequests).Error; err != nil {
		return domain.Task{}, err
	}
	item.PullRequests = make([]domain.PullRequest, 0, len(pullRequests))
	for _, pr := range pullRequests {
		item.PullRequests = append(item.PullRequests, domain.PullRequest{Repository: pr.Repository, Number: pr.Number, URL: pr.URL})
	}
	if !detail {
		return item, nil
	}
	stateHistory, err := store.stateHistory(ctx, "task", id)
	if err != nil {
		return domain.Task{}, err
	}
	item.StateHistory = stateHistory
	readiness, err := store.taskReadiness(ctx, item)
	if err != nil {
		return domain.Task{}, err
	}
	item.Readiness = &readiness
	return item, nil
}

func (store *Store) ListTasks(ctx context.Context, cursor string, limit int, ready bool) (domain.Page[domain.Task], error) {
	query := store.db.WithContext(ctx).Model(&taskRow{}).Where("task.id > ?", cursor)
	if ready {
		query = query.Where("task.state='open'").Order("priority DESC,id")
	} else {
		query = query.Order("id")
	}
	var rows []taskRow
	if err := query.Find(&rows).Error; err != nil {
		return domain.Page[domain.Task]{}, err
	}
	items := make([]domain.Task, 0, limit+1)
	for _, r := range rows {
		t, e := store.getTask(ctx, r.ID, false)
		if e != nil {
			return domain.Page[domain.Task]{}, e
		}
		if ready {
			readiness, err := store.taskReadiness(ctx, t)
			if err != nil {
				return domain.Page[domain.Task]{}, err
			}
			if !readiness.Ready {
				continue
			}
		}
		items = append(items, t)
		if len(items) == limit+1 {
			break
		}
	}
	page := domain.Page[domain.Task]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.NextCursor = items[limit-1].ID
	}
	return page, nil
}

func unmetRequirementDependencies(tx *gorm.DB, taskID string) (int64, error) {
	type dependency struct {
		ID, LifecycleState, ReconciliationState string
		Revision, CurrentRevision               int
	}
	var dependencies []dependency
	query := `WITH RECURSIVE deps(id, revision) AS (
SELECT rd.dependency_id, rd.dependency_revision
FROM task_requirement tr
JOIN requirement_dependency rd ON rd.requirement_id=tr.requirement_id AND rd.requirement_revision=tr.requirement_revision
WHERE tr.task_id=?
UNION
SELECT rd.dependency_id, rd.dependency_revision
FROM requirement_dependency rd
JOIN deps d ON rd.requirement_id=d.id AND rd.requirement_revision=d.revision
)
SELECT d.id,d.revision,r.current_revision,r.lifecycle_state,r.reconciliation_state FROM deps d
LEFT JOIN requirement r ON r.id=d.id
ORDER BY d.id,d.revision`
	if err := tx.Raw(query, taskID).Scan(&dependencies).Error; err != nil {
		return 0, err
	}
	var count int64
	for _, dependency := range dependencies {
		if dependency.CurrentRevision == 0 || dependency.CurrentRevision != dependency.Revision || dependency.LifecycleState != string(domain.Active) {
			count++
			continue
		}
		state, err := effectiveRequirementState(tx, dependency.ID, dependency.CurrentRevision, domain.ReconciliationState(dependency.ReconciliationState))
		if err != nil {
			return 0, err
		}
		if state != domain.Satisfied {
			count++
		}
	}
	return count, nil
}

func (store *Store) taskReadiness(ctx context.Context, item domain.Task) (domain.Readiness, error) {
	blockers := []string{}
	if item.State != "open" {
		blockers = append(blockers, fmt.Sprintf("task state is %s", item.State))
	}
	var leases []leaseRow
	if err := store.db.WithContext(ctx).Where("task_id=? AND expires_at>?", item.ID, now()).Find(&leases).Error; err != nil {
		return domain.Readiness{}, err
	}
	if len(leases) > 0 {
		blockers = append(blockers, fmt.Sprintf("active lease %s belongs to %s", leases[0].LeaseID, leases[0].AgentID))
	}
	type taskDependency struct{ ID, State string }
	var taskDependencies []taskDependency
	if err := store.db.WithContext(ctx).Raw(`SELECT t.id,t.state FROM task_dependency d JOIN task t ON t.id=d.dependency_id WHERE d.task_id=? AND t.state!='complete' ORDER BY t.id`, item.ID).Scan(&taskDependencies).Error; err != nil {
		return domain.Readiness{}, err
	}
	for _, dependency := range taskDependencies {
		blockers = append(blockers, fmt.Sprintf("task dependency %s is %s", dependency.ID, dependency.State))
	}
	type requirementDependency struct {
		ID, LifecycleState, ReconciliationState string
		Revision, CurrentRevision               int
	}
	var requirements []requirementDependency
	query := `WITH RECURSIVE deps(id,revision) AS (
SELECT rd.dependency_id,rd.dependency_revision FROM task_requirement tr JOIN requirement_dependency rd ON rd.requirement_id=tr.requirement_id AND rd.requirement_revision=tr.requirement_revision WHERE tr.task_id=?
UNION SELECT rd.dependency_id,rd.dependency_revision FROM requirement_dependency rd JOIN deps d ON rd.requirement_id=d.id AND rd.requirement_revision=d.revision
) SELECT d.id,d.revision,r.current_revision,r.lifecycle_state,r.reconciliation_state FROM deps d LEFT JOIN requirement r ON r.id=d.id ORDER BY d.id,d.revision`
	if err := store.db.WithContext(ctx).Raw(query, item.ID).Scan(&requirements).Error; err != nil {
		return domain.Readiness{}, err
	}
	for _, requirement := range requirements {
		ref := fmt.Sprintf("%s@%d", requirement.ID, requirement.Revision)
		effectiveState := domain.ReconciliationState(requirement.ReconciliationState)
		if requirement.CurrentRevision != 0 {
			var stateErr error
			effectiveState, stateErr = effectiveRequirementState(store.db.WithContext(ctx), requirement.ID, requirement.CurrentRevision, effectiveState)
			if stateErr != nil {
				return domain.Readiness{}, stateErr
			}
		}
		switch {
		case requirement.CurrentRevision == 0:
			blockers = append(blockers, ref+" does not exist")
		case requirement.CurrentRevision != requirement.Revision:
			blockers = append(blockers, fmt.Sprintf("%s is stale; current revision is %d", ref, requirement.CurrentRevision))
		case requirement.LifecycleState != string(domain.Active):
			blockers = append(blockers, ref+" is retired")
		case effectiveState != domain.Satisfied:
			blockers = append(blockers, fmt.Sprintf("%s is %s", ref, effectiveState))
		}
	}
	var retiredLinks []string
	if err := store.db.WithContext(ctx).Raw(`SELECT r.id FROM task_requirement tr JOIN requirement r ON r.id=tr.requirement_id WHERE tr.task_id=? AND r.lifecycle_state!='active' ORDER BY r.id`, item.ID).Scan(&retiredLinks).Error; err != nil {
		return domain.Readiness{}, err
	}
	for _, id := range retiredLinks {
		blockers = append(blockers, fmt.Sprintf("linked requirement %s is retired", id))
	}
	var nonLeafLinks []string
	nonLeafQuery := `SELECT DISTINCT tr.requirement_id FROM task_requirement tr
WHERE tr.task_id=? AND EXISTS (
  SELECT 1 FROM requirement_refinement rr
  JOIN requirement child ON child.id=rr.child_id
   AND child.current_revision=rr.child_revision
   AND child.lifecycle_state='active'
  WHERE rr.parent_id=tr.requirement_id
    AND rr.parent_revision=tr.requirement_revision
) ORDER BY tr.requirement_id`
	if err := store.db.WithContext(ctx).Raw(nonLeafQuery, item.ID).Scan(&nonLeafLinks).Error; err != nil {
		return domain.Readiness{}, err
	}
	for _, id := range nonLeafLinks {
		blockers = append(blockers, fmt.Sprintf("linked requirement %s is not a leaf", id))
	}
	return domain.Readiness{Ready: len(blockers) == 0, Blockers: blockers}, nil
}

func (store *Store) LeaseTask(ctx context.Context, id, agent string, ttl time.Duration, actor string) (domain.Lease, error) {
	var out domain.Lease
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var task taskRow
		if err := tx.First(&task, "id=?", id).Error; err != nil {
			return ErrNotFound
		}
		if task.State != "open" {
			return ErrConflict
		}
		var retiredLinks int64
		if err := tx.Raw(`SELECT count(*) FROM task_requirement tr JOIN requirement r ON r.id=tr.requirement_id WHERE tr.task_id=? AND r.lifecycle_state!='active'`, id).Scan(&retiredLinks).Error; err != nil {
			return err
		}
		if retiredLinks > 0 {
			return friendlyConflict{message: fmt.Sprintf("task %s links to a retired requirement", id)}
		}
		var nonLeafLinks int64
		if err := tx.Raw(`SELECT count(*) FROM task_requirement tr WHERE tr.task_id=? AND EXISTS (
SELECT 1 FROM requirement_refinement rr
JOIN requirement child ON child.id=rr.child_id AND child.current_revision=rr.child_revision AND child.lifecycle_state='active'
WHERE rr.parent_id=tr.requirement_id AND rr.parent_revision=tr.requirement_revision
)`, id).Scan(&nonLeafLinks).Error; err != nil {
			return err
		}
		if nonLeafLinks > 0 {
			return friendlyConflict{message: fmt.Sprintf("task %s links to a requirement that is not a leaf", id)}
		}
		var blockers int64
		tx.Raw(`SELECT count(*) FROM task_dependency d JOIN task t ON t.id=d.dependency_id WHERE d.task_id=? AND t.state!='complete'`, id).Scan(&blockers)
		if blockers > 0 {
			return friendlyConflict{message: fmt.Sprintf("task %s has incomplete task dependencies", id)}
		}
		blockers, err := unmetRequirementDependencies(tx, id)
		if err != nil {
			return err
		}
		if blockers > 0 {
			return friendlyConflict{message: fmt.Sprintf("task %s has requirement dependencies that are not satisfied at their linked revisions", id)}
		}
		tx.Exec(`DELETE FROM lease WHERE task_id=? AND expires_at<=?`, id, now())
		var count int64
		tx.Model(&leaseRow{}).Where("task_id=?", id).Count(&count)
		if count > 0 {
			return ErrConflict
		}
		fence := task.Fence + 1
		claimed := time.Now().UTC()
		leaseID := fmt.Sprintf("L-%d-%s", claimed.UnixNano(), id)
		row := leaseRow{id, leaseID, agent, fence, claimed.Format(time.RFC3339Nano), claimed.Format(time.RFC3339Nano), claimed.Add(ttl).Format(time.RFC3339Nano)}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		if err := tx.Model(&taskRow{}).Where("id=?", id).Updates(map[string]any{"fence": fence, "updated_at": now()}).Error; err != nil {
			return err
		}
		var requirementIDs []string
		if err := tx.Model(&taskRequirementRow{}).Where("task_id=?", id).Distinct().Pluck("requirement_id", &requirementIDs).Error; err != nil {
			return err
		}
		for _, requirementID := range requirementIDs {
			if err := setRequirementState(tx, requirementID, domain.InProgress, actor); err != nil {
				return err
			}
		}
		if err := audit(ctx, tx, actor, "task.leased", "task", id, map[string]any{"lease": leaseID, "agent": agent}); err != nil {
			return err
		}
		out = domain.Lease{TaskID: id, LeaseID: leaseID, AgentID: agent, Fence: fence, ClaimedAt: claimed, ExpiresAt: claimed.Add(ttl)}
		return nil
	})
	return out, err
}

func (store *Store) ListLeases(ctx context.Context, cursor string, limit int, agent, task string) (domain.Page[domain.Lease], error) {
	query := store.db.WithContext(ctx).Model(&leaseRow{}).
		Where("lease_id > ? AND expires_at > ?", cursor, now()).
		Order("lease_id")
	if agent != "" {
		query = query.Where("agent_id = ?", agent)
	}
	if task != "" {
		query = query.Where("task_id = ?", task)
	}
	var rows []leaseRow
	if err := query.Limit(limit + 1).Find(&rows).Error; err != nil {
		return domain.Page[domain.Lease]{}, err
	}
	page := domain.Page[domain.Lease]{Items: []domain.Lease{}}
	if len(rows) > limit {
		page.NextCursor = rows[limit-1].LeaseID
		rows = rows[:limit]
	}
	for _, row := range rows {
		claimed, err := time.Parse(time.RFC3339Nano, row.ClaimedAt)
		if err != nil {
			return page, err
		}
		expires, err := time.Parse(time.RFC3339Nano, row.ExpiresAt)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, domain.Lease{TaskID: row.TaskID, LeaseID: row.LeaseID, AgentID: row.AgentID, Fence: row.Fence, ClaimedAt: claimed, ExpiresAt: expires})
	}
	return page, nil
}

func (store *Store) Heartbeat(ctx context.Context, id string, fence int, ttl time.Duration, actor string) (domain.Lease, error) {
	var row leaseRow
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&row, "lease_id=? AND fence=? AND expires_at>?", id, fence, now()).Error; err != nil {
			return ErrConflict
		}
		stamp := time.Now().UTC()
		row.HeartbeatAt = stamp.Format(time.RFC3339Nano)
		row.ExpiresAt = stamp.Add(ttl).Format(time.RFC3339Nano)
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		return audit(ctx, tx, actor, "lease.heartbeat", "lease", id, nil)
	})
	claimed, _ := time.Parse(time.RFC3339Nano, row.ClaimedAt)
	expires, _ := time.Parse(time.RFC3339Nano, row.ExpiresAt)
	return domain.Lease{TaskID: row.TaskID, LeaseID: row.LeaseID, AgentID: row.AgentID, Fence: row.Fence, ClaimedAt: claimed, ExpiresAt: expires}, err
}

func (store *Store) Release(ctx context.Context, id string, fence int, actor string) error {
	return store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var lease leaseRow
		if err := tx.First(&lease, "lease_id=? AND fence=?", id, fence).Error; err != nil {
			return ErrConflict
		}
		if err := tx.Delete(&lease).Error; err != nil {
			return err
		}
		if err := refreshTaskRequirements(ctx, tx, lease.TaskID, actor); err != nil {
			return err
		}
		return audit(ctx, tx, actor, "lease.released", "lease", id, nil)
	})
}

func (store *Store) CompleteTask(ctx context.Context, taskID, leaseID string, fence int, commit, actor string) (domain.Task, error) {
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var lease leaseRow
		if err := tx.First(&lease, "task_id=? AND lease_id=? AND fence=? AND expires_at>?", taskID, leaseID, fence, now()).Error; err != nil {
			return ErrConflict
		}
		stamp := now()
		var task taskRow
		if err := tx.First(&task, "id=?", taskID).Error; err != nil {
			return err
		}
		if err := tx.Model(&taskRow{}).Where("id=?", taskID).Updates(map[string]any{"state": "complete", "completed_commit": commit, "completed_at": stamp, "updated_at": stamp}).Error; err != nil {
			return err
		}
		if err := recordState(tx, "task", taskID, "state", task.State, "complete", actor); err != nil {
			return err
		}
		if err := tx.Delete(&lease).Error; err != nil {
			return err
		}
		if err := refreshTaskRequirements(ctx, tx, taskID, actor); err != nil {
			return err
		}
		return audit(ctx, tx, actor, "task.completed", "task", taskID, map[string]any{"commit": commit})
	})
	if err != nil {
		return domain.Task{}, err
	}
	return store.GetTask(ctx, taskID)
}

func refreshTaskRequirements(ctx context.Context, tx *gorm.DB, taskID, actor string) error {
	var links []taskRequirementRow
	if err := tx.Where("task_id=?", taskID).Find(&links).Error; err != nil {
		return err
	}
	for _, link := range links {
		state := string(domain.NotSatisfied)
		var activeLeases int64
		if err := tx.Raw(`SELECT count(*) FROM lease l JOIN task_requirement tr ON tr.task_id=l.task_id WHERE tr.requirement_id=? AND tr.requirement_revision=? AND l.expires_at>?`, link.RequirementID, link.RequirementRevision, now()).Scan(&activeLeases).Error; err != nil {
			return err
		}
		var acceptedReviews int64
		if err := tx.Table("requirement_review").Where("requirement_id=? AND requirement_revision=? AND verdict='accept'", link.RequirementID, link.RequirementRevision).Count(&acceptedReviews).Error; err != nil {
			return err
		}
		var causes int64
		if err := tx.Table("reconciliation_cause").Where("requirement_id=? AND requirement_revision=? AND resolved_at IS NULL", link.RequirementID, link.RequirementRevision).Count(&causes).Error; err != nil {
			return err
		}
		var completedAfterReview int64
		if err := tx.Raw(`SELECT count(*) FROM task t JOIN task_requirement tr ON tr.task_id=t.id WHERE tr.requirement_id=? AND tr.requirement_revision=? AND t.state='complete' AND t.completed_at>COALESCE((SELECT max(reviewed_at) FROM requirement_review WHERE requirement_id=? AND requirement_revision=?),'')`, link.RequirementID, link.RequirementRevision, link.RequirementID, link.RequirementRevision).Scan(&completedAfterReview).Error; err != nil {
			return err
		}
		if activeLeases > 0 {
			state = string(domain.InProgress)
		} else if completedAfterReview > 0 {
			state = string(domain.ReadyForReview)
		} else if causes > 0 {
			state = string(domain.NeedsReconciliation)
		} else if acceptedReviews > 0 {
			state = string(domain.Satisfied)
		}
		var current requirementRow
		if err := tx.First(&current, "id=? AND current_revision=?", link.RequirementID, link.RequirementRevision).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return err
		}
		if err := setRequirementState(tx, link.RequirementID, domain.ReconciliationState(state), actor); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) CloseTask(ctx context.Context, id, actor string) (domain.Task, error) {
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var task taskRow
		if err := tx.First(&task, "id=?", id).Error; err != nil {
			return ErrNotFound
		}
		if task.State != "open" {
			return friendlyConflict{message: fmt.Sprintf("task %s is %s", id, task.State)}
		}
		var leases int64
		if err := tx.Model(&leaseRow{}).Where("task_id=? AND expires_at>?", id, now()).Count(&leases).Error; err != nil {
			return err
		}
		if leases > 0 {
			return friendlyConflict{message: fmt.Sprintf("task %s has an active lease", id)}
		}
		if err := setTaskState(tx, id, "closed", actor); err != nil {
			return err
		}
		if err := refreshTaskRequirements(ctx, tx, id, actor); err != nil {
			return err
		}
		return audit(ctx, tx, actor, "task.closed", "task", id, nil)
	})
	if err != nil {
		return domain.Task{}, err
	}
	return store.GetTask(ctx, id)
}

func (store *Store) ExpireLeases(ctx context.Context, actor string) error {
	return store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var leases []leaseRow
		if err := tx.Where("expires_at<=?", now()).Find(&leases).Error; err != nil {
			return err
		}
		for _, lease := range leases {
			if err := tx.Delete(&lease).Error; err != nil {
				return err
			}
			if err := refreshTaskRequirements(ctx, tx, lease.TaskID, actor); err != nil {
				return err
			}
			if err := audit(ctx, tx, actor, "lease.expired", "lease", lease.LeaseID, map[string]any{"task": lease.TaskID}); err != nil {
				return err
			}
		}
		return nil
	})
}

func (store *Store) NextLeaseExpiry(ctx context.Context) (time.Time, bool, error) {
	type result struct{ Value *string }
	var row result
	if err := store.db.WithContext(ctx).Raw(`SELECT min(expires_at) AS value FROM lease`).Scan(&row).Error; err != nil {
		return time.Time{}, false, err
	}
	if row.Value == nil {
		return time.Time{}, false, nil
	}
	value, err := time.Parse(time.RFC3339Nano, *row.Value)
	return value, true, err
}

func (store *Store) LinkPullRequest(ctx context.Context, taskID string, pr domain.PullRequest, actor string) error {
	return store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var task taskRow
		if err := tx.First(&task, "id=?", taskID).Error; err != nil {
			return ErrNotFound
		}
		id, err := upsertPullRequest(tx, pr)
		if err != nil {
			return err
		}
		if err := tx.Exec(`INSERT OR IGNORE INTO task_pull_request(task_id,pull_request_id) VALUES(?,?)`, taskID, id).Error; err != nil {
			return err
		}
		return audit(ctx, tx, actor, "task.pr_linked", "task", taskID, map[string]any{"url": pr.URL})
	})
}

func upsertPullRequest(tx *gorm.DB, pr domain.PullRequest) (int64, error) {
	var id int64
	err := tx.Raw(`INSERT INTO pull_request(repository,number,url) VALUES(?,?,?) ON CONFLICT(repository,number) DO UPDATE SET url=excluded.url RETURNING id`, pr.Repository, pr.Number, pr.URL).Scan(&id).Error
	return id, err
}

func (store *Store) ListAudit(ctx context.Context, entity, cursor string, limit int) (domain.Page[domain.AuditEvent], error) {
	start, _ := strconv.ParseInt(cursor, 10, 64)
	query := store.db.WithContext(ctx).Table("audit_event").Where("sequence>?", start).Order("sequence").Limit(limit + 1)
	if entity != "" {
		parts := strings.SplitN(entity, ":", 2)
		if len(parts) == 2 {
			query = query.Where("entity_type=? AND entity_id=?", parts[0], parts[1])
		} else {
			query = query.Where("entity_id=?", entity)
		}
	}
	type row struct {
		Sequence                                                                              int64
		OccurredAt, ActorID, CorrelationID, CausationID, Kind, EntityType, EntityID, DataJSON string
	}
	var rows []row
	if err := query.Scan(&rows).Error; err != nil {
		return domain.Page[domain.AuditEvent]{}, err
	}
	page := domain.Page[domain.AuditEvent]{Items: []domain.AuditEvent{}}
	if len(rows) > limit {
		page.NextCursor = strconv.FormatInt(rows[limit-1].Sequence, 10)
		rows = rows[:limit]
	}
	for _, r := range rows {
		data := map[string]any{}
		json.Unmarshal([]byte(r.DataJSON), &data)
		at, _ := time.Parse(time.RFC3339Nano, r.OccurredAt)
		page.Items = append(page.Items, domain.AuditEvent{Sequence: r.Sequence, OccurredAt: at, ActorID: r.ActorID, CorrelationID: r.CorrelationID, CausationID: r.CausationID, Kind: r.Kind, EntityType: r.EntityType, EntityID: r.EntityID, Data: data})
	}
	return page, nil
}
func (store *Store) PruneAudit(ctx context.Context, before time.Time) error {
	requestID := fmt.Sprintf("audit-prune-%d", time.Now().UTC().UnixNano())
	ctx = ports.WithRequestIDs(ctx, requestID, requestID)
	return store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Exec("DELETE FROM audit_event WHERE occurred_at < ?", before.UTC().Format(time.RFC3339Nano))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		return audit(ctx, tx, "system", "audit.pruned", "audit", "retention", map[string]any{"deleted": result.RowsAffected})
	})
}
