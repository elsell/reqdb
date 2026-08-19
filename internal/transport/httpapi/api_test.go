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
