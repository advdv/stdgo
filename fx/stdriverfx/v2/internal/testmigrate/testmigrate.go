// Package testmigrate migrates the test databases of package stdriverfx with
// River's own migrations, into the schema the clients use by default.
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
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"
)

// Schema is where Migrator puts River's tables: the default of
// stdriverfx.Config.Schema.
const Schema = "river"

// Migrator creates River's tables in [Schema] with River's migrations.
var Migrator pgtestdb.Migrator = migrator{}

type migrator struct{}

// newMigrator builds River's migrator on db, which may be nil when no
// migration runs.
func newMigrator(db *sql.DB) (*rivermigrate.Migrator[*sql.Tx], error) {
	mig, err := rivermigrate.New(riverdatabasesql.New(db), &rivermigrate.Config{Schema: Schema})

	return mig, errors.Wrap(err, "new river migrator")
}

// Hash identifies River's migrations, so pgtestdb migrates its template
// database again whenever an upgrade of River changes them.
func (migrator) Hash() (string, error) {
	mig, err := newMigrator(nil)
	if err != nil {
		return "", err
	}

	hash := sha256.New()
	for _, m := range mig.AllVersions() {
		fmt.Fprintf(hash, "%d\n%s\n", m.Version, m.SQLUp)
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// Migrate creates [Schema] and runs River's migrations into it.
func (migrator) Migrate(ctx context.Context, db *sql.DB, _ pgtestdb.Config) error {
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA "+pgx.Identifier{Schema}.Sanitize()); err != nil {
		return errors.Wrap(err, "create river schema")
	}

	mig, err := newMigrator(db)
	if err != nil {
		return err
	}

	if _, err := mig.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return errors.Wrap(err, "migrate river")
	}

	return nil
}
