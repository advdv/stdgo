// Package stdpgtest provides testing against a postgresql database.
package stdpgtest

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/stretchr/testify/require"
)

// SetupPgxPool will init a isolated test database from a connection string and a snapshot sql file.
func SetupPgxPool(ctx context.Context, tb testing.TB, snapshotFile, connString string) *pgxpool.Pool {
	tb.Helper()

	migrator := SnapshotMigrator[*sql.DB](snapshotFile)
	testCfg := NewPgxTestDB(tb, migrator, connString, nil)

	pcfg, err := pgxpool.ParseConfig(testCfg.URL())
	require.NoError(tb, err)

	rw, err := pgxpool.NewWithConfig(ctx, pcfg)
	require.NoError(tb, err)
	tb.Cleanup(func() { rw.Close() })

	return rw
}

// NewPgxTestDB will use the pgtestdb package to migrate, creates a isolated database and returns the
// connection string to that database..
func NewPgxTestDB(
	tb testing.TB,
	migrator pgtestdb.Migrator,
	setupConnStr string,
	testRole *pgtestdb.Role,
) *pgtestdb.Config {
	tb.Helper()

	cfg, err := pgx.ParseConfig(setupConnStr)
	require.NoError(tb, err)

	urlParsed, err := url.Parse(cfg.ConnString())
	require.NoError(tb, err)

	return pgtestdb.Custom(tb, pgtestdb.Config{
		DriverName: "pgx",
		User:       cfg.User,
		Password:   cfg.Password,
		Host:       cfg.Host,
		Database:   cfg.Database,
		Port:       fmt.Sprintf("%d", cfg.Port),
		Options:    urlParsed.RawQuery,
		TestRole:   testRole,
	}, migrator)
}
