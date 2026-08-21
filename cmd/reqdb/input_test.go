package main

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestRequirementInputFromFlags(t *testing.T) {
	input, err := requirementInput([]string{
		"create", "SWR-TEST-001",
		"--level", "software",
		"--title", "Test input",
		"--statement", "The software shall accept test input.",
		"--refines", "SYR-TEST-001@1",
		"--depends-on=SWR-BASE-001@2",
		"--depends-on", "SWR-CLOCK-001@1",
	}, "create")
	if err != nil {
		t.Fatal(err)
	}
	if input.Schema != "requirement/v1" || input.ID != "SWR-TEST-001" || input.Revision != 1 {
		t.Fatalf("unexpected identity: %#v", input)
	}
	if !reflect.DeepEqual(input.Links.Refines, []string{"SYR-TEST-001@1"}) {
		t.Fatalf("unexpected refinement links: %v", input.Links.Refines)
	}
	wantDependencies := []string{"SWR-BASE-001@2", "SWR-CLOCK-001@1"}
	if !reflect.DeepEqual(input.Links.DependsOn, wantDependencies) {
		t.Fatalf("got dependencies %v, want %v", input.Links.DependsOn, wantDependencies)
	}
}

func TestRequirementUpdateInputSetsNextRevision(t *testing.T) {
	input, err := requirementInput([]string{
		"update", "SWR-TEST-001", "--expected", "4", "--level", "software",
		"--title", "New title", "--statement", "The software shall use the new title.",
	}, "update")
	if err != nil {
		t.Fatal(err)
	}
	if input.Revision != 5 {
		t.Fatalf("got revision %d, want 5", input.Revision)
	}
}

func TestTaskInputFromFlags(t *testing.T) {
	input, err := taskInput([]string{
		"create", "T-42", "--title", "Implement input", "--description", "Implement the input modes.",
		"--priority", "60", "--requirement", "SWR-TEST-001@1:implement",
		"--requirement=SWR-OTHER-001@2:reconcile", "--depends-on", "T-41",
	})
	if err != nil {
		t.Fatal(err)
	}
	if input.Schema != "task/v1" || input.ID != "T-42" || input.Priority != 60 {
		t.Fatalf("unexpected task: %#v", input)
	}
	if len(input.Requirements) != 2 || input.Requirements[1].Purpose != "reconcile" {
		t.Fatalf("unexpected requirement links: %#v", input.Requirements)
	}
	if !reflect.DeepEqual(input.DependsOn, []string{"T-41"}) {
		t.Fatalf("unexpected dependencies: %v", input.DependsOn)
	}
}

func TestInputModesCannotBeCombined(t *testing.T) {
	_, err := requirementInput([]string{"create", "--from-file", "input.yaml", "--title", "Conflict"}, "create")
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("got error %v", err)
	}
	_, err = taskInput([]string{"create", "--from-file", "input.yaml", "--priority", "50"})
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("got error %v", err)
	}
}

func TestReviewInputFromFlags(t *testing.T) {
	input, err := reviewInput([]string{"review", "SWR-TEST-001", "--commit", strings.Repeat("a", 40), "--verdict", "reject", "--task", "T-1", "--finding", "First finding", "--finding", "Second finding"})
	if err != nil {
		t.Fatal(err)
	}
	if input.Verdict != "reject" || input.TaskID != "T-1" || len(input.Findings) != 2 {
		t.Fatalf("unexpected review input: %+v", input)
	}
}

func TestReviewInputFromStandardInput(t *testing.T) {
	original := os.Stdin
	file, err := os.CreateTemp(t.TempDir(), "review-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString("commit: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nverdict: reject\nfindings:\n  - message: Missing result.\n    path: result.go\n    line: 7\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	os.Stdin = file
	defer func() { os.Stdin = original }()
	input, err := reviewInput([]string{"review", "SWR-TEST-001", "--from-file", "-"})
	if err != nil {
		t.Fatal(err)
	}
	if input.Verdict != "reject" || len(input.Findings) != 1 || input.Findings[0].Line != 7 {
		t.Fatalf("unexpected review input: %+v", input)
	}
}

func TestRequirementInputFromStandardInput(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdin
	os.Stdin = reader
	t.Cleanup(func() {
		os.Stdin = previous
		reader.Close()
	})
	content := "schema: requirement/v1\nid: BR-TEST-001\nlevel: business\nrevision: 1\ntitle: Test\nstatement: The organization shall test input.\nlinks:\n  refines: []\n"
	if _, err := writer.WriteString(content); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	input, err := requirementInput([]string{"create", "-f", "-"}, "create")
	if err != nil {
		t.Fatal(err)
	}
	if input.ID != "BR-TEST-001" {
		t.Fatalf("got ID %q", input.ID)
	}
}
