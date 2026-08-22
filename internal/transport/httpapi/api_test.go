package httpapi_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elsell/reqdb/internal/application"
	"github.com/elsell/reqdb/internal/domain"
	"github.com/elsell/reqdb/internal/observability"
	"github.com/elsell/reqdb/internal/ports"
	"github.com/elsell/reqdb/internal/store/sqlite"
	"github.com/elsell/reqdb/internal/transport/httpapi"
)

func TestAPIEnvelopeAndCorrelationID(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "reqdb.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	api := httpapi.API{Service: application.Service{Store: store, Auth: application.AllowAll{}}}
	server := httptest.NewServer(api.Handler())
	t.Cleanup(server.Close)

	body := map[string]any{"schema": "requirement/v1", "id": "BR-API-001", "level": "business", "revision": 1, "title": "API", "statement": "The organization shall expose one API.", "links": map[string]any{"refines": []string{}}}
	value, _ := json.Marshal(body)
	request, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/requirements", bytes.NewReader(value))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Correlation-ID", "test-correlation")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("status %d", response.StatusCode)
	}
	if response.Header.Get("X-Correlation-ID") != "test-correlation" {
		t.Fatal("correlation ID did not propagate")
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
		Meta struct {
			CorrelationID string `json:"correlation_id"`
		} `json:"meta"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data) == 0 || envelope.Meta.CorrelationID != "test-correlation" {
		t.Fatal("invalid response envelope")
	}
	var item struct {
		ReconciliationState domain.ReconciliationState `json:"reconciliation_state"`
		Workability         map[string]any             `json:"workability"`
	}
	if err := json.Unmarshal(envelope.Data, &item); err != nil {
		t.Fatal(err)
	}
	if item.ReconciliationState != domain.PendingReview || item.Workability["work_status"] != "managed_through_children" {
		t.Fatalf("unexpected requirement state: %+v", item)
	}
	if _, oldField := item.Workability["disposition"]; oldField {
		t.Fatal("response contains obsolete disposition field")
	}
}

func TestBearerAuthenticationAndProjectRoutes(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "reqdb.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	api := httpapi.API{Service: application.Service{Store: store, Auth: application.AllowAll{}}, Password: "secret"}
	server := httptest.NewServer(api.Handler())
	t.Cleanup(server.Close)
	response, err := http.Get(server.URL + "/v1/projects")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized || response.Header.Get("WWW-Authenticate") == "" {
		t.Fatalf("unauthorized response: %d", response.StatusCode)
	}
	body := strings.NewReader(`{"id":"alpha","name":"Alpha"}`)
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/projects", body)
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create project status %d", response.StatusCode)
	}
	request, _ = http.NewRequest(http.MethodGet, server.URL+"/v1/projects/alpha/requirements", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("nested resource status %d", response.StatusCode)
	}
}

func TestReviewReadEndpoints(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "reqdb.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	input := domain.RequirementInput{Schema: "requirement/v1", ID: "STR-REVIEW-API-001", Level: "stakeholder", Revision: 1, Title: "Read reviews", Statement: "The service shall return one review."}
	if _, err := store.CreateRequirement(ctx, input, "tester"); err != nil {
		t.Fatal(err)
	}
	item, _, err := store.ReviewRequirement(ctx, domain.ReviewInput{Requirement: domain.RequirementRef{ID: input.ID}, Commit: strings.Repeat("a", 40), Verdict: "accept"}, "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	reviewID := item.Reviews[0].ID
	api := httpapi.API{Service: application.Service{Store: store, Auth: application.AllowAll{}}}
	server := httptest.NewServer(api.Handler())
	t.Cleanup(server.Close)

	response, err := http.Get(server.URL + "/v1/reviews/" + reviewID)
	if err != nil {
		t.Fatal(err)
	}
	var one struct {
		Data domain.Review `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&one); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || one.Data.ID != reviewID || one.Data.Requirement != (domain.RequirementRef{ID: input.ID, Revision: 1}) {
		t.Fatalf("unexpected review response: status=%d data=%+v", response.StatusCode, one.Data)
	}

	response, err = http.Get(server.URL + "/v1/requirements/" + input.ID + "@1/reviews?limit=1")
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Data []domain.Review `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || len(list.Data) != 1 || list.Data[0].ID != reviewID {
		t.Fatalf("unexpected review list: status=%d data=%+v", response.StatusCode, list.Data)
	}
}

func TestEventStreamPublishesChanges(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "reqdb.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	broker := observability.NewBroker()
	api := httpapi.API{Service: application.Service{Store: store, Auth: application.AllowAll{}}, Events: broker}
	server := httptest.NewServer(api.Handler())
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/events", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("content type %q", response.Header.Get("Content-Type"))
	}

	broker.Record(context.Background(), ports.Event{Name: "requirement.created"})
	scanner := bufio.NewScanner(response.Body)
	found := false
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), `"name":"requirement.created"`) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("change event was not received: %v", scanner.Err())
	}
}

func TestListActiveLeases(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "reqdb.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	task := domain.TaskInput{Schema: "task/v1", ID: "T-LEASE", Title: "Lease", Description: "Test active lease listing.", Priority: 1}
	if _, err := store.CreateTask(ctx, task, "tester"); err != nil {
		t.Fatal(err)
	}
	lease, err := store.LeaseTask(ctx, task.ID, "agent-a", time.Minute, "tester")
	if err != nil {
		t.Fatal(err)
	}
	api := httpapi.API{Service: application.Service{Store: store, Auth: application.AllowAll{}}}
	server := httptest.NewServer(api.Handler())
	t.Cleanup(server.Close)

	response, err := http.Get(server.URL + "/v1/leases?agent=agent-a&task=T-LEASE")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result struct {
		Data []domain.Lease `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || len(result.Data) != 1 || result.Data[0].LeaseID != lease.LeaseID {
		t.Fatalf("unexpected lease list: status=%d data=%+v", response.StatusCode, result.Data)
	}
}
