package ports

import (
	"context"
	"time"

	"github.com/elsell/reqdb/internal/domain"
)

type contextKey string

const correlationKey contextKey = "correlation"
const causationKey contextKey = "causation"

func WithRequestIDs(ctx context.Context, correlation, causation string) context.Context {
	ctx = context.WithValue(ctx, correlationKey, correlation)
	return context.WithValue(ctx, causationKey, causation)
}

func CorrelationID(ctx context.Context) string {
	value, _ := ctx.Value(correlationKey).(string)
	return value
}

func CausationID(ctx context.Context) string {
	value, _ := ctx.Value(causationKey).(string)
	return value
}

type Store interface {
	CreateRequirement(context.Context, domain.RequirementInput, string) (domain.Requirement, error)
	UpdateRequirement(context.Context, domain.RequirementInput, int, string) (domain.Requirement, error)
	GetRequirement(context.Context, domain.RequirementRef) (domain.Requirement, error)
	ListRequirements(context.Context, string, int, string, string) (domain.Page[domain.Requirement], error)
	ConfirmRequirement(context.Context, domain.RequirementRef, string, string, string) (domain.Requirement, error)
	Trace(context.Context, string) ([]domain.Requirement, error)
	Impact(context.Context, string) ([]domain.Requirement, error)
	TasksForRequirements(context.Context, []domain.RequirementRef) ([]domain.Task, error)
	CreateTask(context.Context, domain.TaskInput, string) (domain.Task, error)
	GetTask(context.Context, string) (domain.Task, error)
	ListTasks(context.Context, string, int, bool) (domain.Page[domain.Task], error)
	LeaseTask(context.Context, string, string, time.Duration, string) (domain.Lease, error)
	ListLeases(context.Context, string, int, string, string) (domain.Page[domain.Lease], error)
	Heartbeat(context.Context, string, int, time.Duration, string) (domain.Lease, error)
	Release(context.Context, string, int, string) error
	CompleteTask(context.Context, string, string, int, string, string) (domain.Task, error)
	LinkPullRequest(context.Context, string, domain.PullRequest, string) error
	ListAudit(context.Context, string, string, int) (domain.Page[domain.AuditEvent], error)
	PruneAudit(context.Context, time.Time) error
	Close() error
}

type Authorizer interface {
	Authorize(context.Context, domain.Identity, string, string) error
}

type Event struct {
	Sequence      uint64         `json:"sequence"`
	Name          string         `json:"name"`
	CorrelationID string         `json:"correlation_id"`
	CausationID   string         `json:"causation_id"`
	Fields        map[string]any `json:"fields,omitempty"`
}

type EventSink interface {
	Record(context.Context, Event)
}

type EventSource interface {
	Subscribe() (<-chan Event, func())
}
