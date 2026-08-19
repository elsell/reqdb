package application_test

import (
	"strings"
	"testing"

	"github.com/elsell/reqdb/internal/application"
)

func TestDecodeRequirementRejectsUnknownField(t *testing.T) {
	input := `schema: requirement/v1
id: BR-TEST-001
level: business
revision: 1
title: Test
statement: The organization shall test the system.
links:
  refines: []
unexpected: true
`
	if _, err := application.DecodeRequirement(strings.NewReader(input)); err == nil {
		t.Fatal("expected an unknown-field error")
	}
}

func TestDecodeRequirementRejectsAlias(t *testing.T) {
	input := `schema: requirement/v1
id: BR-TEST-001
level: business
revision: 1
title: &title Test
statement: The organization shall test the system.
links:
  refines: []
copy: *title
`
	if _, err := application.DecodeRequirement(strings.NewReader(input)); err == nil {
		t.Fatal("expected an alias error")
	}
}
