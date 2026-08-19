package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

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

func TestTraceColorsRequirementMetadata(t *testing.T) {
	root := domain.Requirement{ID: "BR-TEST-001", CurrentRevision: 1, ReconciliationState: domain.Implemented, Revision: domain.RequirementRevision{Revision: 1, Level: "business", Title: "Root"}}
	child := domain.Requirement{ID: "STR-TEST-001", CurrentRevision: 1, ReconciliationState: domain.Unimplemented, Revision: domain.RequirementRevision{Revision: 1, Level: "stakeholder", Title: "Child", Parents: []domain.RequirementRef{{ID: root.ID, Revision: 1}}}}
	data, _ := json.Marshal(domain.RequirementGraph{Requirements: []domain.Requirement{root, child}})
	output := captureOutput(t, func() error { return printRequirementTreeWithColor(data, true) })
	for _, value := range []string{
		"\x1b[90m└── \x1b[0m",
		"\x1b[1mSTR-TEST-001\x1b[0m",
		"\x1b[90m@1\x1b[0m",
		"\x1b[97;44m stakeholder \x1b[0m",
		"\x1b[97;100m unimplemented \x1b[0m",
	} {
		if !strings.Contains(output, value) {
			t.Fatalf("colored trace does not contain %q: %q", value, output)
		}
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

func TestLeaseListUsesTable(t *testing.T) {
	lease := domain.Lease{LeaseID: "L-1", TaskID: "T-1", AgentID: "agent-a", Fence: 2, ClaimedAt: time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC), ExpiresAt: time.Date(2026, 8, 18, 12, 30, 0, 0, time.UTC)}
	data, _ := json.Marshal([]domain.Lease{lease})
	output := captureOutput(t, func() error { return printHuman(http.MethodGet, "/v1/leases?agent=agent-a", data) })
	if !strings.Contains(output, "LEASE") || !strings.Contains(output, "L-1") || !strings.Contains(output, "agent-a") {
		t.Fatalf("lease list is not a table: %s", output)
	}
}

func TestTraceShowsDependencyLinksSeparately(t *testing.T) {
	base := domain.Requirement{ID: "SWR-BASE-001", CurrentRevision: 1, Revision: domain.RequirementRevision{Revision: 1, Level: "software", Title: "Base"}}
	target := domain.Requirement{ID: "SWR-TARGET-001", CurrentRevision: 1, Revision: domain.RequirementRevision{Revision: 1, Level: "software", Title: "Target", Dependencies: []domain.RequirementRef{{ID: base.ID, Revision: 1}}}}
	data, _ := json.Marshal(domain.RequirementGraph{Requirements: []domain.Requirement{base, target}})
	output := captureOutput(t, func() error { return printHuman(http.MethodGet, "/v1/trace", data) })
	for _, value := range []string{"Dependency links:", "REQUIREMENT", "DEPENDS ON", "SWR-TARGET-001@1", "SWR-BASE-001@1"} {
		if !strings.Contains(output, value) {
			t.Fatalf("trace does not contain %q: %s", value, output)
		}
	}
}
