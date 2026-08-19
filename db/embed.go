package db

import _ "embed"

// Schema is the version 1 database schema.
//
//go:embed schema.sql
var Schema string

// RequirementDependencyMigration adds requirement dependencies to an existing
// version 1 database.
//
//go:embed requirement_dependency.sql
var RequirementDependencyMigration string
