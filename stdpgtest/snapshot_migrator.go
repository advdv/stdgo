package stdpgtest

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"

	"github.com/peterldowns/pgtestdb"
)

// SnapshotMigrator loads a migration from a postgres dump file.
type SnapshotMigrator[T interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}] []byte

// Hash implements the migrater interface.
func (m SnapshotMigrator[T]) Hash() (string, error) {
	return fmt.Sprintf("%x", sha256.Sum256(m)), nil
}

// Migrate performs the actual migration.
func (m SnapshotMigrator[T]) Migrate(ctx context.Context, db T, _ pgtestdb.Config) error {
	if _, err := db.ExecContext(ctx, string(m)); err != nil {
		return fmt.Errorf("failed to execute snapshot sql: %w", err)
	}

	return nil
}

var _ pgtestdb.Migrator = SnapshotMigrator[*sql.DB]("")
