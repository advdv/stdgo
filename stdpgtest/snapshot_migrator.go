package stdpgtest

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"

	"github.com/peterldowns/pgtestdb"
)

// SnapshotMigrator loads a migration from a postgres dump file.
type SnapshotMigrator[T interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}] string

// Hash implements the migrater interface.
func (m SnapshotMigrator[T]) Hash() (string, error) {
	data, err := m.readSnapshot()
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

// Migrate performs the actual migration.
func (m SnapshotMigrator[T]) Migrate(ctx context.Context, db T, _ pgtestdb.Config) error {
	data, err := m.readSnapshot()
	if err != nil {
		return err
	}

	if _, err := db.ExecContext(ctx, string(data)); err != nil {
		return fmt.Errorf("failed to execute snapshot sql: %w", err)
	}

	return nil
}

func (m SnapshotMigrator[T]) readSnapshot() ([]byte, error) {
	data, err := os.ReadFile(string(m))
	if err != nil {
		return nil, fmt.Errorf("failed to read snapshot file: %w", err)
	}

	return data, nil
}

var _ pgtestdb.Migrator = SnapshotMigrator[*sql.DB]("")
