// Package testmigrate migrates the test databases of package stdriverprofx
// with River's own migrations, River Pro's included, into the schema the
// clients use by default.
package testmigrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"

	"github.com/cockroachdb/errors"
	"github.com/jackc/pgx/v5"
	"github.com/peterldowns/pgtestdb"
	"github.com/riverqueue/river/rivermigrate"
	"riverqueue.com/riverpro/driver/riverprodatabasesql"
)

// Schema is where Migrator puts River's tables: the default of
// stdriverfx.Config.Schema.
const Schema = "river"

// lines are the migration lines that River Pro's clients need, in the order
// they migrate: open-source River's, then River Pro's.
var lines = []string{"main", "pro"}

// Migrator creates River's tables, River Pro's included, in [Schema] with
// River's migrations.
var Migrator pgtestdb.Migrator = migrator{}

type migrator struct{}

// newMigrator builds River's migrator for line on db, which may be nil when no
// migration runs.
func newMigrator(db *sql.DB, line string) (*rivermigrate.Migrator[*sql.Tx], error) {
	mig, err := rivermigrate.New(riverprodatabasesql.New(db), &rivermigrate.Config{Line: line, Schema: Schema})

	return mig, errors.Wrapf(err, "new river migrator for line %q", line)
}

// Hash identifies the migrations of every line, so pgtestdb migrates its
// template database again whenever an upgrade of River or River Pro changes
// them.
func (migrator) Hash() (string, error) {
	hash := sha256.New()

	for _, line := range lines {
		mig, err := newMigrator(nil, line)
		if err != nil {
			return "", err
		}

		for _, m := range mig.AllVersions() {
			fmt.Fprintf(hash, "%s %d\n%s\n", line, m.Version, m.SQLUp)
		}
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// Migrate creates [Schema] and runs the migrations of every line into it.
func (migrator) Migrate(ctx context.Context, db *sql.DB, _ pgtestdb.Config) error {
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA "+pgx.Identifier{Schema}.Sanitize()); err != nil {
		return errors.Wrap(err, "create river schema")
	}

	for _, line := range lines {
		mig, err := newMigrator(db, line)
		if err != nil {
			return err
		}

		if _, err := mig.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
			return errors.Wrapf(err, "migrate river line %q", line)
		}
	}

	return nil
}
