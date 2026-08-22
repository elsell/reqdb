package domain

import "testing"

func TestRequirementValidationRejectsRemovedSoftwareLevel(t *testing.T) {
	input := RequirementInput{
		Schema:    "requirement/v1",
		ID:        "SWR-OLD-001",
		Level:     "software",
		Revision:  1,
		Title:     "Old requirement",
		Statement: "The software shall use an obsolete requirement level.",
	}
	if err := input.Validate(); err == nil {
		t.Fatal("software requirement passed validation")
	}
}
