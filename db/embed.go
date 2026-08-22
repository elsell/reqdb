package db

import _ "embed"

// Schema is the version 1 database schema.
//
//go:embed schema.sql
var Schema string
