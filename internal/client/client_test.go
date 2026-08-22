package client_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elsell/reqdb/internal/client"
)

func TestClientAddsBearerAndProjectScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/alpha/tasks" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "meta": map[string]any{"correlation_id": "test"}})
	}))
	defer server.Close()
	api := client.Client{BaseURL: server.URL, Token: "secret", Project: "alpha", HTTP: server.Client()}
	if _, err := api.Do(context.Background(), http.MethodGet, "/v1/tasks", nil); err != nil {
		t.Fatal(err)
	}
}
