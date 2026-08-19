package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/elsell/reqdb/internal/domain"
)

func captureOutput(t *testing.T, run func() error) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = writer
	err = run()
	_ = writer.Close()
	os.Stdout = old
	if err != nil {
		t.Fatal(err)
	}
	value, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return string(value)
}

func TestRequirementListUsesTable(t *testing.T) {
	items := []domain.Requirement{{ID: "BR-TEST-001", CurrentRevision: 1, ReconciliationState: domain.Unimplemented, Revision: domain.RequirementRevision{Level: "business", Title: "Test result"}}}
	data, _ := json.Marshal(items)
	output := captureOutput(t, func() error { return printHuman(http.MethodGet, "/v1/requirements", data) })
	for _, text := range []string{"ID", "RECONCILIATION", "BR-TEST-001", "Test result"} {
		if !strings.Contains(output, text) {
			t.Fatalf("output does not contain %q: %s", text, output)
		}
	}
	if strings.Contains(output, "{") {
		t.Fatalf("output contains raw JSON: %s", output)
	}
}

func TestRequirementDetailUsesFields(t *testing.T) {
	item := domain.Requirement{ID: "BR-TEST-001", CurrentRevision: 1, ReconciliationState: domain.Implemented, Revision: domain.RequirementRevision{Revision: 1, Level: "business", Title: "Test result", Statement: "The organization shall get one result."}}
	data, _ := json.Marshal(item)
	output := captureOutput(t, func() error { return printHuman(http.MethodPost, "/v1/requirements", data) })
	for _, text := range []string{"Requirement BR-TEST-001@1", "Title:", "Reconciliation:", "Statement:"} {
		if !strings.Contains(output, text) {
			t.Fatalf("output does not contain %q: %s", text, output)
		}
	}
}

func TestTraceUsesTreeBranches(t *testing.T) {
	root := domain.Requirement{ID: "BR-TEST-001", CurrentRevision: 1, ReconciliationState: domain.Unimplemented, Revision: domain.RequirementRevision{Revision: 1, Level: "business", Title: "Root"}}
	child := domain.Requirement{ID: "STR-TEST-001", CurrentRevision: 1, ReconciliationState: domain.Unimplemented, Revision: domain.RequirementRevision{Revision: 1, Level: "stakeholder", Title: "Child", Parents: []domain.RequirementRef{{ID: root.ID, Revision: 1}}}}
	data, _ := json.Marshal(domain.RequirementGraph{Requirements: []domain.Requirement{root, child}})
	output := captureOutput(t, func() error { return printHuman(http.MethodGet, "/v1/trace/BR-TEST-001", data) })
	if !strings.Contains(output, "└── STR-TEST-001@1") {
		t.Fatalf("trace does not contain a tree branch: %s", output)
	}
}

func TestTraceShowsLinkedTask(t *testing.T) {
	root := domain.Requirement{ID: "SWR-TEST-001", CurrentRevision: 1, ReconciliationState: domain.Unimplemented, Revision: domain.RequirementRevision{Revision: 1, Level: "software", Title: "Calculate"}}
	task := domain.Task{ID: "T-1", Title: "Implement calculation", State: "open", Requirements: []domain.TaskRequirementInput{{Requirement: "SWR-TEST-001@1", Purpose: "implement"}}}
	data, _ := json.Marshal(domain.RequirementGraph{Requirements: []domain.Requirement{root}, Tasks: []domain.Task{task}})
	output := captureOutput(t, func() error { return printHuman(http.MethodGet, "/v1/trace/SWR-TEST-001", data) })
	if !strings.Contains(output, "└── T-1") {
		t.Fatalf("trace does not contain its linked task: %s", output)
	}
}
