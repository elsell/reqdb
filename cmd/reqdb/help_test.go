package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestActionHelp(t *testing.T) {
	help := helpFor([]string{"requirement", "update", "--help"})
	if !strings.Contains(help, "--expected REVISION") {
		t.Fatal("requirement update help does not show its required revision")
	}
	if strings.Contains(help, "software") || strings.Contains(help, "SWR-") {
		t.Fatal("help contains the removed requirement level")
	}
}

func TestNormalizeGlobalArgs(t *testing.T) {
	input := []string{"--server", "http://localhost:8080", "requirement", "list", "--json", "--level", "system"}
	want := []string{"requirement", "list", "--level", "system", "--server", "http://localhost:8080", "--json"}
	if got := normalizeGlobalArgs(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
