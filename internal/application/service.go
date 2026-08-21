package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/elsell/reqdb/internal/domain"
	"github.com/elsell/reqdb/internal/ports"
	"gopkg.in/yaml.v3"
)

func WithRequestIDs(ctx context.Context, correlation, causation string) context.Context {
	return ports.WithRequestIDs(ctx, correlation, causation)
}
func CorrelationID(ctx context.Context) string {
	return ports.CorrelationID(ctx)
}
func CausationID(ctx context.Context) string {
	return ports.CausationID(ctx)
}
func NewID() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(value)
}

type AllowAll struct{}

func (AllowAll) Authorize(context.Context, domain.Identity, string, string) error { return nil }

type Service struct {
	Store     ports.Store
	Auth      ports.Authorizer
	Events    ports.EventSink
	LeaseWake chan<- struct{}
}

func (service Service) wakeLeaseScheduler() {
	if service.LeaseWake == nil {
		return
	}
	select {
	case service.LeaseWake <- struct{}{}:
	default:
	}
}

func (service Service) authorize(ctx context.Context, actor, permission, resource string) error {
	if actor == "" {
		actor = "anonymous"
	}
	return service.Auth.Authorize(ctx, domain.Identity{ActorID: actor}, permission, resource)
}
func (service Service) event(ctx context.Context, name string, fields map[string]any) {
	if service.Events != nil {
		service.Events.Record(ctx, ports.Event{Name: name, CorrelationID: CorrelationID(ctx), CausationID: CausationID(ctx), Fields: fields})
	}
}

func (service Service) WatchEvents(ctx context.Context, actor string) error {
	return service.authorize(ctx, actor, "event.read", "")
}

func DecodeRequirement(reader io.Reader) (domain.RequirementInput, error) {
	var node yaml.Node
	decoder := yaml.NewDecoder(reader)
	if err := decoder.Decode(&node); err != nil {
		return domain.RequirementInput{}, err
	}
	if err := safeYAML(&node); err != nil {
		return domain.RequirementInput{}, err
	}
	var input domain.RequirementInput
	if err := decodeStrict(&node, &input); err != nil {
		return input, err
	}
	return input, input.Validate()
}
func DecodeTask(reader io.Reader) (domain.TaskInput, error) {
	var node yaml.Node
	decoder := yaml.NewDecoder(reader)
	if err := decoder.Decode(&node); err != nil {
		return domain.TaskInput{}, err
	}
	if err := safeYAML(&node); err != nil {
		return domain.TaskInput{}, err
	}
	var input domain.TaskInput
	if err := decodeStrict(&node, &input); err != nil {
		return input, err
	}
	return input, input.Validate()
}
func decodeStrict(node *yaml.Node, target any) error {
	value, err := yaml.Marshal(node)
	if err != nil {
		return err
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(value)))
	decoder.KnownFields(true)
	return decoder.Decode(target)
}
func safeYAML(node *yaml.Node) error {
	if node.Alias != nil {
		return errors.New("YAML aliases are not allowed")
	}
	if node.Tag != "" && !strings.HasPrefix(node.Tag, "!!") && !strings.HasPrefix(node.Tag, "tag:yaml.org,2002:") {
		return errors.New("custom YAML tags are not allowed")
	}
	if node.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i].Value
			if seen[key] {
				return fmt.Errorf("duplicate YAML key %q", key)
			}
			seen[key] = true
		}
	}
	for _, child := range node.Content {
		if err := safeYAML(child); err != nil {
			return err
		}
	}
	return nil
}

func (service Service) CreateRequirement(ctx context.Context, input domain.RequirementInput, actor string) (domain.Requirement, error) {
	if err := service.authorize(ctx, actor, "requirement.create", input.ID); err != nil {
		return domain.Requirement{}, err
	}
	if err := input.Validate(); err != nil {
		return domain.Requirement{}, err
	}
	result, err := service.Store.CreateRequirement(ctx, input, actor)
	if err == nil {
		service.event(ctx, "requirement.created", map[string]any{"requirement_id": input.ID})
	}
	return result, err
}
func (service Service) UpdateRequirement(ctx context.Context, input domain.RequirementInput, expected int, actor string) (domain.Requirement, error) {
	if err := service.authorize(ctx, actor, "requirement.update", input.ID); err != nil {
		return domain.Requirement{}, err
	}
	if err := input.Validate(); err != nil {
		return domain.Requirement{}, err
	}
	result, err := service.Store.UpdateRequirement(ctx, input, expected, actor)
	if err == nil {
		service.event(ctx, "requirement.revised", map[string]any{"requirement_id": input.ID, "revision": input.Revision})
	}
	return result, err
}
func (service Service) GetRequirement(ctx context.Context, ref domain.RequirementRef, actor string) (domain.Requirement, error) {
	if err := service.authorize(ctx, actor, "requirement.read", ref.ID); err != nil {
		return domain.Requirement{}, err
	}
	return service.Store.GetRequirement(ctx, ref)
}
func (service Service) ListRequirements(ctx context.Context, cursor string, limit int, level, state, actor string) (domain.Page[domain.Requirement], error) {
	if err := service.authorize(ctx, actor, "requirement.read", ""); err != nil {
		return domain.Page[domain.Requirement]{}, err
	}
	return service.Store.ListRequirements(ctx, cursor, limit, level, state)
}
func (service Service) ListWorkableRequirements(ctx context.Context, cursor string, limit int, actor string) (domain.Page[domain.Requirement], error) {
	if err := service.authorize(ctx, actor, "requirement.read", ""); err != nil {
		return domain.Page[domain.Requirement]{}, err
	}
	return service.Store.ListWorkableRequirements(ctx, cursor, limit)
}
func (service Service) ReviewRequirement(ctx context.Context, input domain.ReviewInput, actor string) (domain.Requirement, error) {
	if err := service.authorize(ctx, actor, "requirement.review", input.Requirement.ID); err != nil {
		return domain.Requirement{}, err
	}
	if err := domain.ValidateCommit(input.Commit); err != nil {
		return domain.Requirement{}, err
	}
	if input.Verdict != "accept" && input.Verdict != "reject" {
		return domain.Requirement{}, errors.New("verdict must be accept or reject")
	}
	if input.Verdict == "reject" && len(input.Findings) == 0 {
		return domain.Requirement{}, errors.New("a rejected review requires at least one finding")
	}
	for _, finding := range input.Findings {
		if strings.TrimSpace(finding.Message) == "" || finding.Line < 0 || (finding.Line > 0 && strings.TrimSpace(finding.Path) == "") {
			return domain.Requirement{}, errors.New("each finding requires a message; a line requires a path")
		}
	}
	item, created, err := service.Store.ReviewRequirement(ctx, input, actor)
	if err == nil && created {
		service.event(ctx, "requirement.reviewed", map[string]any{"requirement_id": input.Requirement.ID, "commit": input.Commit, "verdict": input.Verdict})
	}
	return item, err
}
func (service Service) GetReview(ctx context.Context, id, actor string) (domain.Review, error) {
	if err := service.authorize(ctx, actor, "review.read", id); err != nil {
		return domain.Review{}, err
	}
	return service.Store.GetReview(ctx, id)
}
func (service Service) ListReviews(ctx context.Context, requirement domain.RequirementRef, cursor string, limit int, actor string) (domain.Page[domain.Review], error) {
	if err := service.authorize(ctx, actor, "review.read", requirement.ID); err != nil {
		return domain.Page[domain.Review]{}, err
	}
	if requirement.ID == "" {
		return domain.Page[domain.Review]{}, errors.New("requirement ID is required")
	}
	return service.Store.ListReviews(ctx, requirement, cursor, limit)
}
func (service Service) RetireRequirement(ctx context.Context, id, actor string) (domain.Requirement, error) {
	if err := service.authorize(ctx, actor, "requirement.retire", id); err != nil {
		return domain.Requirement{}, err
	}
	item, err := service.Store.RetireRequirement(ctx, id, actor)
	if err == nil {
		service.event(ctx, "requirement.retired", map[string]any{"requirement_id": id})
	}
	return item, err
}
func (service Service) Trace(ctx context.Context, root, actor string) (domain.RequirementGraph, error) {
	if err := service.authorize(ctx, actor, "requirement.read", root); err != nil {
		return domain.RequirementGraph{}, err
	}
	return service.graph(ctx, root, actor, false)
}
func (service Service) Impact(ctx context.Context, root, actor string) (domain.RequirementGraph, error) {
	if err := service.authorize(ctx, actor, "requirement.read", root); err != nil {
		return domain.RequirementGraph{}, err
	}
	return service.graph(ctx, root, actor, true)
}
func (service Service) graph(ctx context.Context, root, actor string, impact bool) (domain.RequirementGraph, error) {
	var requirements []domain.Requirement
	var err error
	if impact {
		requirements, err = service.Store.Impact(ctx, root)
	} else {
		requirements, err = service.Store.Trace(ctx, root)
	}
	if err != nil {
		return domain.RequirementGraph{}, err
	}
	if err := service.authorize(ctx, actor, "task.read", ""); err != nil {
		return domain.RequirementGraph{}, err
	}
	refs := make([]domain.RequirementRef, 0, len(requirements))
	for _, item := range requirements {
		refs = append(refs, domain.RequirementRef{ID: item.ID, Revision: item.Revision.Revision})
	}
	tasks, err := service.Store.TasksForRequirements(ctx, refs)
	if err != nil {
		return domain.RequirementGraph{}, err
	}
	return domain.RequirementGraph{Requirements: requirements, Tasks: tasks}, nil
}
func (service Service) CreateTask(ctx context.Context, input domain.TaskInput, actor string) (domain.Task, error) {
	if err := service.authorize(ctx, actor, "task.create", input.ID); err != nil {
		return domain.Task{}, err
	}
	if err := input.Validate(); err != nil {
		return domain.Task{}, err
	}
	task, err := service.Store.CreateTask(ctx, input, actor)
	if err == nil {
		service.event(ctx, "task.created", map[string]any{"task_id": input.ID})
	}
	return task, err
}
func (service Service) GetTask(ctx context.Context, id, actor string) (domain.Task, error) {
	if err := service.authorize(ctx, actor, "task.read", id); err != nil {
		return domain.Task{}, err
	}
	return service.Store.GetTask(ctx, id)
}
func (service Service) ListTasks(ctx context.Context, cursor string, limit int, workable bool, actor string) (domain.Page[domain.Task], error) {
	if err := service.authorize(ctx, actor, "task.read", ""); err != nil {
		return domain.Page[domain.Task]{}, err
	}
	return service.Store.ListTasks(ctx, cursor, limit, workable)
}
func (service Service) LeaseTask(ctx context.Context, id, agent string, ttl time.Duration, actor string) (domain.Lease, error) {
	if err := service.authorize(ctx, actor, "task.lease", id); err != nil {
		return domain.Lease{}, err
	}
	lease, err := service.Store.LeaseTask(ctx, id, agent, ttl, actor)
	if err == nil {
		service.event(ctx, "task.leased", map[string]any{"task_id": id, "agent_id": agent})
		service.wakeLeaseScheduler()
	}
	return lease, err
}
func (service Service) ListLeases(ctx context.Context, cursor string, limit int, agent, task, actor string) (domain.Page[domain.Lease], error) {
	if err := service.authorize(ctx, actor, "lease.read", ""); err != nil {
		return domain.Page[domain.Lease]{}, err
	}
	return service.Store.ListLeases(ctx, cursor, limit, agent, task)
}
func (service Service) Heartbeat(ctx context.Context, id string, fence int, ttl time.Duration, actor string) (domain.Lease, error) {
	if err := service.authorize(ctx, actor, "lease.heartbeat", id); err != nil {
		return domain.Lease{}, err
	}
	lease, err := service.Store.Heartbeat(ctx, id, fence, ttl, actor)
	if err == nil {
		service.event(ctx, "lease.heartbeat", map[string]any{"lease_id": id, "task_id": lease.TaskID})
		service.wakeLeaseScheduler()
	}
	return lease, err
}
func (service Service) Release(ctx context.Context, id string, fence int, actor string) error {
	if err := service.authorize(ctx, actor, "lease.release", id); err != nil {
		return err
	}
	err := service.Store.Release(ctx, id, fence, actor)
	if err == nil {
		service.event(ctx, "lease.released", map[string]any{"lease_id": id})
		service.wakeLeaseScheduler()
	}
	return err
}
func (service Service) CompleteTask(ctx context.Context, id, lease string, fence int, commit, actor string) (domain.Task, error) {
	if err := service.authorize(ctx, actor, "task.complete", id); err != nil {
		return domain.Task{}, err
	}
	if err := domain.ValidateCommit(commit); err != nil {
		return domain.Task{}, err
	}
	task, err := service.Store.CompleteTask(ctx, id, lease, fence, commit, actor)
	if err == nil {
		service.event(ctx, "task.completed", map[string]any{"task_id": id, "commit": commit})
		service.wakeLeaseScheduler()
	}
	return task, err
}
func (service Service) CloseTask(ctx context.Context, id, actor string) (domain.Task, error) {
	if err := service.authorize(ctx, actor, "task.close", id); err != nil {
		return domain.Task{}, err
	}
	task, err := service.Store.CloseTask(ctx, id, actor)
	if err == nil {
		service.event(ctx, "task.closed", map[string]any{"task_id": id})
	}
	return task, err
}

func (service Service) ExpireLeases(ctx context.Context) error {
	err := service.Store.ExpireLeases(ctx, "system")
	if err == nil {
		service.event(ctx, "leases.expired", nil)
	}
	return err
}
func (service Service) LinkPullRequest(ctx context.Context, id string, pr domain.PullRequest, actor string) error {
	if err := service.authorize(ctx, actor, "task.link_pr", id); err != nil {
		return err
	}
	if pr.Repository == "" || pr.URL == "" || pr.Number < 1 {
		return errors.New("pull request repository, number, and URL are required")
	}
	err := service.Store.LinkPullRequest(ctx, id, pr, actor)
	if err == nil {
		service.event(ctx, "task.pr_linked", map[string]any{"task_id": id, "pull_request": pr.URL})
	}
	return err
}
func (service Service) ListAudit(ctx context.Context, entity, cursor string, limit int, actor string) (domain.Page[domain.AuditEvent], error) {
	if err := service.authorize(ctx, actor, "audit.read", entity); err != nil {
		return domain.Page[domain.AuditEvent]{}, err
	}
	return service.Store.ListAudit(ctx, entity, cursor, limit)
}

func Render(requirements []domain.Requirement) string {
	var text strings.Builder
	for _, item := range requirements {
		fmt.Fprintf(&text, "## %s: %s\n\n", item.ID, item.Revision.Title)
		fmt.Fprintf(&text, "- Level: %s\n- Revision: %d\n- Lifecycle: %s\n- Reconciliation: %s\n", item.Revision.Level, item.Revision.Revision, item.LifecycleState, item.ReconciliationState)
		if item.Workability != nil {
			fmt.Fprintf(&text, "- Workable: %t\n- Disposition: %s\n", item.Workability.Workable, item.Workability.Disposition)
			for _, reason := range item.Workability.Reasons {
				fmt.Fprintf(&text, "  - Reason: %s\n", reason)
			}
		}
		if len(item.Revision.Parents) > 0 {
			fmt.Fprint(&text, "- Refines:")
			for _, parent := range item.Revision.Parents {
				fmt.Fprintf(&text, " `%s@%d`", parent.ID, parent.Revision)
			}
			fmt.Fprintln(&text)
		}
		if len(item.Revision.Dependencies) > 0 {
			fmt.Fprint(&text, "- Depends on:")
			for _, dependency := range item.Revision.Dependencies {
				fmt.Fprintf(&text, " `%s@%d`", dependency.ID, dependency.Revision)
			}
			fmt.Fprintln(&text)
		}
		fmt.Fprintf(&text, "\n%s\n\n", item.Revision.Statement)
	}
	return text.String()
}
