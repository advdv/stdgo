// Package testsnapshot provides migration based on a snapshot.
package testsnapshot

import (
	"database/sql"
	_ "embed"

	"github.com/advdv/stdgo/stdpgtest"
)

// Migrator that migrates with a snapshot.
var Migrator = stdpgtest.SnapshotMigrator[*sql.DB](snapshot)

//go:embed snapshot.sql
var snapshot []byte
