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
	if err := migrate(db); err != nil {
		return nil, err
	}
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

type leaseRow struct {
	TaskID                            string `gorm:"primaryKey"`
	LeaseID, AgentID                  string
	Fence                             int
	ClaimedAt, HeartbeatAt, ExpiresAt string
}

func (leaseRow) TableName() string { return "lease" }

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func audit(ctx context.Context, tx *gorm.DB, actor, kind, entityType, entityID string, data any) error {
	value, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return tx.Exec(`INSERT INTO audit_event (occurred_at,actor_id,correlation_id,causation_id,kind,entity_type,entity_id,data_json) VALUES (?,?,?,?,?,?,?,?)`, now(), actor, ports.CorrelationID(ctx), ports.CausationID(ctx), kind, entityType, entityID, string(value)).Error
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
		if err := tx.Create(&requirementRow{ID: input.ID, CurrentRevision: input.Revision, LifecycleState: string(domain.Active), ReconciliationState: string(domain.Unimplemented), CreatedAt: stamp, UpdatedAt: stamp}).Error; err != nil {
			return friendlyConflict{message: fmt.Sprintf("requirement %s already exists", input.ID)}
		}
		if err := addRevision(tx, input, actor); err != nil {
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
		if err := tx.Model(&requirementRow{}).Where("id=? AND current_revision=?", input.ID, expected).Updates(map[string]any{"current_revision": input.Revision, "reconciliation_state": string(domain.Unimplemented), "updated_at": now()}).Error; err != nil {
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
			if err := tx.Model(&requirementRow{}).Where("id=?", item.ID).Updates(map[string]any{"reconciliation_state": string(domain.NeedsReconciliation), "updated_at": now()}).Error; err != nil {
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
	var root requirementRow
	if err := store.db.WithContext(ctx).First(&root, "id=?", ref.ID).Error; err != nil {
		return domain.Requirement{}, ErrNotFound
	}
	if ref.Revision == 0 {
		ref.Revision = root.CurrentRevision
	}
	var rev revisionRow
	if err := store.db.WithContext(ctx).First(&rev, "requirement_id=? AND revision=?", ref.ID, ref.Revision).Error; err != nil {
		return domain.Requirement{}, ErrNotFound
	}
	var rows []refinementRow
	if err := store.db.WithContext(ctx).Where("child_id=? AND child_revision=?", ref.ID, ref.Revision).Find(&rows).Error; err != nil {
		return domain.Requirement{}, err
	}
	parents := make([]domain.RequirementRef, 0, len(rows))
	for _, row := range rows {
		parents = append(parents, domain.RequirementRef{ID: row.ParentID, Revision: row.ParentRevision})
	}
	var dependencyRows []requirementDependencyRow
	if err := store.db.WithContext(ctx).Where("requirement_id=? AND requirement_revision=?", ref.ID, ref.Revision).Find(&dependencyRows).Error; err != nil {
		return domain.Requirement{}, err
	}
	dependencies := make([]domain.RequirementRef, 0, len(dependencyRows))
	for _, row := range dependencyRows {
		dependencies = append(dependencies, domain.RequirementRef{ID: row.DependencyID, Revision: row.DependencyRevision})
	}
	created, _ := time.Parse(time.RFC3339Nano, rev.CreatedAt)
	return domain.Requirement{ID: root.ID, CurrentRevision: root.CurrentRevision, LifecycleState: domain.LifecycleState(root.LifecycleState), ReconciliationState: domain.ReconciliationState(root.ReconciliationState), Revision: domain.RequirementRevision{RequirementID: rev.RequirementID, Revision: rev.Revision, Level: rev.Level, Title: rev.Title, Statement: rev.Statement, Parents: parents, Dependencies: dependencies, CreatedAt: created, ActorID: rev.ActorID}}, nil
}

func (store *Store) ListRequirements(ctx context.Context, cursor string, limit int, level, state string) (domain.Page[domain.Requirement], error) {
	query := store.db.WithContext(ctx).Model(&requirementRow{}).Where("id > ?", cursor).Order("id").Limit(limit + 1)
	if state != "" {
		query = query.Where("reconciliation_state=?", state)
	}
	if level != "" {
		query = query.Joins("JOIN requirement_revision rr ON rr.requirement_id=requirement.id AND rr.revision=requirement.current_revision").Where("rr.level=?", level)
	}
	var rows []requirementRow
	if err := query.Find(&rows).Error; err != nil {
		return domain.Page[domain.Requirement]{}, err
	}
	page := domain.Page[domain.Requirement]{Items: []domain.Requirement{}}
	if len(rows) > limit {
		page.NextCursor = rows[limit-1].ID
		rows = rows[:limit]
	}
	for _, row := range rows {
		item, err := store.GetRequirement(ctx, domain.RequirementRef{ID: row.ID})
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, item)
	}
	return page, nil
}

func (store *Store) ListReadyRequirements(ctx context.Context, cursor string, limit int) (domain.Page[domain.Requirement], error) {
	query := store.db.WithContext(ctx).Model(&requirementRow{}).
		Where("requirement.id > ?", cursor).
		Where("requirement.lifecycle_state=?", domain.Active).
		Where("requirement.reconciliation_state IN ?", []domain.ReconciliationState{domain.Unimplemented, domain.NeedsReconciliation}).
		Where(`NOT EXISTS (
SELECT 1 FROM task_requirement tr
JOIN task t ON t.id=tr.task_id
WHERE tr.requirement_id=requirement.id
  AND tr.requirement_revision=requirement.current_revision
  AND t.state!='complete'
)`).
		Where(requirementReadyDependenciesSQL).
		Order("requirement.id").Limit(limit + 1)
	var rows []requirementRow
	if err := query.Find(&rows).Error; err != nil {
		return domain.Page[domain.Requirement]{}, err
	}
	page := domain.Page[domain.Requirement]{Items: []domain.Requirement{}}
	if len(rows) > limit {
		page.NextCursor = rows[limit-1].ID
		rows = rows[:limit]
	}
	for _, row := range rows {
		item, err := store.GetRequirement(ctx, domain.RequirementRef{ID: row.ID})
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, item)
	}
	return page, nil
}

const requirementReadyDependenciesSQL = `NOT EXISTS (
WITH RECURSIVE deps(id, revision) AS (
  SELECT rd.dependency_id, rd.dependency_revision
  FROM requirement_dependency rd
  WHERE rd.requirement_id=requirement.id
    AND rd.requirement_revision=requirement.current_revision
  UNION
  SELECT rd.dependency_id, rd.dependency_revision
  FROM requirement_dependency rd
  JOIN deps d ON rd.requirement_id=d.id AND rd.requirement_revision=d.revision
)
SELECT 1 FROM deps d
LEFT JOIN requirement r ON r.id=d.id
WHERE r.id IS NULL
   OR r.current_revision!=d.revision
   OR r.lifecycle_state!='active'
   OR r.reconciliation_state!='implemented'
)`

func (store *Store) ConfirmRequirement(ctx context.Context, ref domain.RequirementRef, commit, result, actor string) (domain.Requirement, error) {
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
		if result == "" {
			result = "code_changed"
		}
		if result != "code_changed" && result != "existing_code_confirmed" {
			return errors.New("invalid confirmation result")
		}
		if err := tx.Exec(`INSERT INTO reconciliation_confirmation (requirement_id,requirement_revision,result,commit_sha,confirmed_at,actor_id) VALUES (?,?,?,?,?,?)`, ref.ID, ref.Revision, result, commit, now(), actor).Error; err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE reconciliation_cause SET resolved_at=? WHERE requirement_id=? AND requirement_revision=? AND resolved_at IS NULL`, now(), ref.ID, ref.Revision).Error; err != nil {
			return err
		}
		if err := tx.Model(&requirementRow{}).Where("id=?", ref.ID).Updates(map[string]any{"reconciliation_state": string(domain.Implemented), "updated_at": now()}).Error; err != nil {
			return err
		}
		return audit(ctx, tx, actor, "requirement.confirmed", "requirement", ref.ID, map[string]any{"revision": ref.Revision, "commit": commit, "result": result})
	})
	if err != nil {
		return domain.Requirement{}, err
	}
	return store.GetRequirement(ctx, ref)
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
			if err := tx.Model(&requirementRow{}).Where("id=? AND lifecycle_state='active'", item.ID).Updates(map[string]any{"reconciliation_state": string(domain.NeedsReconciliation), "updated_at": now()}).Error; err != nil {
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
		item, err := store.GetRequirement(ctx, domain.RequirementRef{ID: id})
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
		task, err := store.GetTask(ctx, id)
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
	return domain.Task{ID: row.ID, Version: row.Version, Title: row.Title, Description: row.Description, Priority: row.Priority, State: row.State, Fence: row.Fence, CompletedCommit: commit, Requirements: reqs, DependsOn: deps}, nil
}

func (store *Store) ListTasks(ctx context.Context, cursor string, limit int, ready bool) (domain.Page[domain.Task], error) {
	query := store.db.WithContext(ctx).Model(&taskRow{}).Where("task.id > ?", cursor)
	if ready {
		query = query.Where("task.state='open'").Where(`NOT EXISTS (SELECT 1 FROM lease WHERE lease.task_id=task.id AND lease.expires_at>?)`, now()).Where(`NOT EXISTS (SELECT 1 FROM task_dependency d JOIN task t ON t.id=d.dependency_id WHERE d.task_id=task.id AND t.state!='complete')`).Where(`NOT EXISTS (SELECT 1 FROM task_requirement tr JOIN requirement r ON r.id=tr.requirement_id WHERE tr.task_id=task.id AND r.lifecycle_state!='active')`).Where(requirementDependenciesReadySQL).Order("priority DESC,id")
	} else {
		query = query.Order("id")
	}
	var rows []taskRow
	if err := query.Limit(limit + 1).Find(&rows).Error; err != nil {
		return domain.Page[domain.Task]{}, err
	}
	page := domain.Page[domain.Task]{Items: []domain.Task{}}
	if len(rows) > limit {
		page.NextCursor = rows[limit-1].ID
		rows = rows[:limit]
	}
	for _, r := range rows {
		t, e := store.GetTask(ctx, r.ID)
		if e != nil {
			return page, e
		}
		page.Items = append(page.Items, t)
	}
	return page, nil
}

const requirementDependenciesReadySQL = `NOT EXISTS (
WITH RECURSIVE deps(id, revision) AS (
  SELECT rd.dependency_id, rd.dependency_revision
  FROM task_requirement tr
  JOIN requirement_dependency rd ON rd.requirement_id=tr.requirement_id AND rd.requirement_revision=tr.requirement_revision
  WHERE tr.task_id=task.id
  UNION
  SELECT rd.dependency_id, rd.dependency_revision
  FROM requirement_dependency rd
  JOIN deps d ON rd.requirement_id=d.id AND rd.requirement_revision=d.revision
)
SELECT 1 FROM deps d
LEFT JOIN requirement r ON r.id=d.id
WHERE r.id IS NULL OR r.current_revision!=d.revision OR r.lifecycle_state!='active' OR r.reconciliation_state!='implemented'
)`

func unmetRequirementDependencies(tx *gorm.DB, taskID string) (int64, error) {
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
SELECT count(*) FROM deps d
LEFT JOIN requirement r ON r.id=d.id
WHERE r.id IS NULL OR r.current_revision!=d.revision OR r.lifecycle_state!='active' OR r.reconciliation_state!='implemented'`
	var count int64
	return count, tx.Raw(query, taskID).Scan(&count).Error
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
			return friendlyConflict{message: fmt.Sprintf("task %s has requirement dependencies that are not implemented at their linked revisions", id)}
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
		tx.Model(&taskRow{}).Where("id=?", id).Updates(map[string]any{"fence": fence, "updated_at": now()})
		tx.Exec(`UPDATE requirement SET reconciliation_state='in_progress',updated_at=? WHERE id IN (SELECT requirement_id FROM task_requirement WHERE task_id=?)`, now(), id)
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
		if err := refreshTaskRequirements(tx, lease.TaskID); err != nil {
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
		if err := tx.Model(&taskRow{}).Where("id=?", taskID).Updates(map[string]any{"state": "complete", "completed_commit": commit, "completed_at": stamp, "updated_at": stamp}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&lease).Error; err != nil {
			return err
		}
		if err := refreshTaskRequirements(tx, taskID); err != nil {
			return err
		}
		return audit(ctx, tx, actor, "task.completed", "task", taskID, map[string]any{"commit": commit})
	})
	if err != nil {
		return domain.Task{}, err
	}
	return store.GetTask(ctx, taskID)
}

func refreshTaskRequirements(tx *gorm.DB, taskID string) error {
	var links []taskRequirementRow
	if err := tx.Where("task_id=?", taskID).Find(&links).Error; err != nil {
		return err
	}
	for _, link := range links {
		state := string(domain.Unimplemented)
		var confirmations int64
		if err := tx.Table("reconciliation_confirmation").Where("requirement_id=? AND requirement_revision=?", link.RequirementID, link.RequirementRevision).Count(&confirmations).Error; err != nil {
			return err
		}
		var causes int64
		if err := tx.Table("reconciliation_cause").Where("requirement_id=? AND requirement_revision=? AND resolved_at IS NULL", link.RequirementID, link.RequirementRevision).Count(&causes).Error; err != nil {
			return err
		}
		if causes > 0 {
			state = string(domain.NeedsReconciliation)
		} else if confirmations > 0 {
			state = string(domain.Implemented)
		}
		if err := tx.Model(&requirementRow{}).Where("id=? AND current_revision=?", link.RequirementID, link.RequirementRevision).Updates(map[string]any{"reconciliation_state": state, "updated_at": now()}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) LinkPullRequest(ctx context.Context, taskID string, pr domain.PullRequest, actor string) error {
	return store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var task taskRow
		if err := tx.First(&task, "id=?", taskID).Error; err != nil {
			return ErrNotFound
		}
		var id int64
		if err := tx.Raw(`INSERT INTO pull_request(repository,number,url) VALUES(?,?,?) ON CONFLICT(repository,number) DO UPDATE SET url=excluded.url RETURNING id`, pr.Repository, pr.Number, pr.URL).Scan(&id).Error; err != nil {
			return err
		}
		if err := tx.Exec(`INSERT OR IGNORE INTO task_pull_request(task_id,pull_request_id) VALUES(?,?)`, taskID, id).Error; err != nil {
			return err
		}
		return audit(ctx, tx, actor, "task.pr_linked", "task", taskID, map[string]any{"url": pr.URL})
	})
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
