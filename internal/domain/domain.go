package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")

type ReconciliationState string

const (
	Unimplemented       ReconciliationState = "unimplemented"
	InProgress          ReconciliationState = "in_progress"
	Implemented         ReconciliationState = "implemented"
	NeedsReconciliation ReconciliationState = "needs_reconciliation"
)

type RequirementRef struct {
	ID       string `json:"id" yaml:"-"`
	Revision int    `json:"revision" yaml:"-"`
}

func ParseRequirementRef(value string) (RequirementRef, error) {
	parts := strings.Split(value, "@")
	if len(parts) != 2 {
		return RequirementRef{}, fmt.Errorf("requirement reference must have ID@REVISION")
	}
	var revision int
	if _, err := fmt.Sscanf(parts[1], "%d", &revision); err != nil || revision < 1 {
		return RequirementRef{}, fmt.Errorf("requirement revision must be positive")
	}
	return RequirementRef{ID: parts[0], Revision: revision}, nil
}

type RequirementInput struct {
	Schema    string `json:"schema" yaml:"schema"`
	ID        string `json:"id" yaml:"id"`
	Level     string `json:"level" yaml:"level"`
	Revision  int    `json:"revision" yaml:"revision"`
	Title     string `json:"title" yaml:"title"`
	Statement string `json:"statement" yaml:"statement"`
	Links     struct {
		Refines []string `json:"refines" yaml:"refines"`
	} `json:"links" yaml:"links"`
}

type Requirement struct {
	ID                  string              `json:"id"`
	CurrentRevision     int                 `json:"current_revision"`
	ReconciliationState ReconciliationState `json:"reconciliation_state"`
	Revision            RequirementRevision `json:"revision"`
}

type RequirementRevision struct {
	RequirementID string           `json:"requirement_id"`
	Revision      int              `json:"revision"`
	Level         string           `json:"level"`
	Title         string           `json:"title"`
	Statement     string           `json:"statement"`
	Parents       []RequirementRef `json:"parents"`
	CreatedAt     time.Time        `json:"created_at"`
	ActorID       string           `json:"actor_id"`
}

var idPatterns = map[string]*regexp.Regexp{
	"business":    regexp.MustCompile(`^BR-[A-Z0-9]+(?:-[A-Z0-9]+)*$`),
	"stakeholder": regexp.MustCompile(`^STR-[A-Z0-9]+(?:-[A-Z0-9]+)*$`),
	"system":      regexp.MustCompile(`^SYR-[A-Z0-9]+(?:-[A-Z0-9]+)*$`),
	"software":    regexp.MustCompile(`^SWR-[A-Z0-9]+(?:-[A-Z0-9]+)*$`),
}

var parentPrefixes = map[string]string{
	"stakeholder": "BR-", "system": "STR-", "software": "SYR-",
}

func (input RequirementInput) Validate() error {
	if input.Schema != "requirement/v1" {
		return errors.New("schema must be requirement/v1")
	}
	pattern, ok := idPatterns[input.Level]
	if !ok || !pattern.MatchString(input.ID) {
		return errors.New("requirement ID does not match its level")
	}
	if input.Revision < 1 || strings.TrimSpace(input.Title) == "" {
		return errors.New("revision and title are required")
	}
	if strings.Count(input.Statement, "shall") != 1 {
		return errors.New("statement must contain one lowercase shall")
	}
	if input.Level == "business" && len(input.Links.Refines) != 0 {
		return errors.New("a business requirement cannot refine another requirement")
	}
	if input.Level != "business" && len(input.Links.Refines) == 0 {
		return errors.New("a non-business requirement must refine a parent")
	}
	for _, value := range input.Links.Refines {
		ref, err := ParseRequirementRef(value)
		if err != nil || !strings.HasPrefix(ref.ID, parentPrefixes[input.Level]) {
			return errors.New("parent requirement has the wrong level or format")
		}
	}
	return nil
}

type TaskRequirementInput struct {
	Requirement string `json:"requirement" yaml:"requirement"`
	Purpose     string `json:"purpose" yaml:"purpose"`
}

type TaskInput struct {
	Schema       string                 `json:"schema" yaml:"schema"`
	ID           string                 `json:"id" yaml:"id"`
	Title        string                 `json:"title" yaml:"title"`
	Description  string                 `json:"description" yaml:"description"`
	Priority     int                    `json:"priority" yaml:"priority"`
	Requirements []TaskRequirementInput `json:"requirements" yaml:"requirements"`
	DependsOn    []string               `json:"depends_on" yaml:"depends_on"`
}

func (input TaskInput) Validate() error {
	if input.Schema != "task/v1" || !regexp.MustCompile(`^T-[0-9]+$`).MatchString(input.ID) {
		return errors.New("task schema or ID is invalid")
	}
	if strings.TrimSpace(input.Title) == "" || strings.TrimSpace(input.Description) == "" {
		return errors.New("task title and description are required")
	}
	if input.Priority < 0 || input.Priority > 100 || len(input.Requirements) == 0 {
		return errors.New("task priority or requirements are invalid")
	}
	for _, link := range input.Requirements {
		if _, err := ParseRequirementRef(link.Requirement); err != nil {
			return err
		}
		if link.Purpose != "implement" && link.Purpose != "reconcile" {
			return errors.New("task requirement purpose is invalid")
		}
	}
	for _, dependency := range input.DependsOn {
		if dependency == input.ID {
			return errors.New("a task cannot depend on itself")
		}
	}
	return nil
}

type Task struct {
	ID              string                 `json:"id"`
	Version         int                    `json:"version"`
	Title           string                 `json:"title"`
	Description     string                 `json:"description"`
	Priority        int                    `json:"priority"`
	State           string                 `json:"state"`
	Fence           int                    `json:"fence"`
	CompletedCommit string                 `json:"completed_commit,omitempty"`
	Requirements    []TaskRequirementInput `json:"requirements"`
	DependsOn       []string               `json:"depends_on"`
}

type Lease struct {
	TaskID    string    `json:"task_id"`
	LeaseID   string    `json:"lease_id"`
	AgentID   string    `json:"agent_id"`
	Fence     int       `json:"fence"`
	ClaimedAt time.Time `json:"claimed_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type PullRequest struct {
	Repository string `json:"repository"`
	Number     int    `json:"number"`
	URL        string `json:"url"`
}

type AuditEvent struct {
	Sequence      int64          `json:"sequence"`
	OccurredAt    time.Time      `json:"occurred_at"`
	ActorID       string         `json:"actor_id"`
	CorrelationID string         `json:"correlation_id"`
	CausationID   string         `json:"causation_id"`
	Kind          string         `json:"kind"`
	EntityType    string         `json:"entity_type"`
	EntityID      string         `json:"entity_id"`
	Data          map[string]any `json:"data"`
}

type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

type RequirementGraph struct {
	Requirements []Requirement `json:"requirements"`
	Tasks        []Task        `json:"tasks"`
}

type Identity struct {
	ActorID string
}
