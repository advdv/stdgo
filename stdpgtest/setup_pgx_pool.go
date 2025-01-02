// Package stdpgtest provides testing against a postgresql database.
package stdpgtest

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/stretchr/testify/require"
)

// SetupPgxPool will init a isolated test database from a connection string and a snapshot sql file.
func SetupPgxPool(ctx context.Context, tb testing.TB, snapshotFile, connString string) *pgxpool.Pool {
	tb.Helper()

	migrator := SnapshotMigrater[*sql.DB](snapshotFile)
	connString = PgxTestDBConnString(tb, migrator, connString)

	pcfg, err := pgxpool.ParseConfig(connString)
	require.NoError(tb, err)

	rw, err := pgxpool.NewWithConfig(ctx, pcfg)
	require.NoError(tb, err)
	tb.Cleanup(func() { rw.Close() })

	return rw
}

// PgxTestDBConnString will use the pgtestdb package to migrate, creates a isolated database and returns the
// connection string to that database..
func PgxTestDBConnString(tb testing.TB, migrator pgtestdb.Migrator, connString string) string {
	tb.Helper()

	cfg, err := pgx.ParseConfig(connString)
	require.NoError(tb, err)

	return pgtestdb.Custom(tb, pgtestdb.Config{
		DriverName: "pgx",
		User:       cfg.User,
		Password:   cfg.Password,
		Host:       cfg.Host,
		Port:       fmt.Sprintf("%d", cfg.Port),
		Options:    "sslmode=disable",
		TestRole: &pgtestdb.Role{
			Username:     "postgres",
			Password:     "postgres",
			Capabilities: pgtestdb.DefaultRoleCapabilities,
		},
	}, migrator).URL()
}
