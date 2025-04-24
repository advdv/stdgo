package stdtxpgxv5_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/advdv/stdgo/stdtx"
	"github.com/advdv/stdgo/stdtx/stdtxpgxv5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/stretchr/testify/require"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestSetup(t *testing.T) {
	ctx, drv := setup(t)
	require.NotNil(t, drv)
	require.NotNil(t, ctx)
}

func TestSetupTx(t *testing.T) {
	ctx, drv := setup(t,
		stdtxpgxv5.DiscourageSeqScan(true),
		stdtxpgxv5.BeginWithSQL(
			func(_ context.Context, sql *strings.Builder, _ pgx.Tx) (*strings.Builder, error) {
				sql.WriteString("SET LOCAL foo.bar = 'dar';")
				return sql, nil
			}))

	tx, err := drv.BeginTx(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)

	var str string
	require.NoError(t, tx.QueryRow(ctx, `SHOW foo.bar`).Scan(&str))
	require.Equal(t, "dar", str)

	var dss string
	require.NoError(t, tx.QueryRow(ctx, `SHOW enable_seqscan`).Scan(&dss))
	require.Equal(t, "off", dss)
}

func setup(tb testing.TB, opts ...stdtxpgxv5.Option) (context.Context, stdtx.Driver[pgx.Tx]) {
	tb.Helper()

	cfg, err := pgx.ParseConfig(`postgresql://postgres:postgres@localhost:5440/postgres`)
	require.NoError(tb, err)

	pgtCfg := pgtestdb.Custom(tb, pgtestdb.Config{
		DriverName: "pgx",
		Host:       cfg.Host,
		Port:       fmt.Sprintf("%d", cfg.Port),
		User:       cfg.User,
		Password:   cfg.Password,
		Database:   cfg.Database,
	}, pgtestdb.NoopMigrator{})

	db, err := pgxpool.New(tb.Context(), pgtCfg.URL())
	require.NoError(tb, err)
	tb.Cleanup(db.Close)

	ctx := tb.Context()

	return ctx, stdtxpgxv5.New(db, opts...)
}
