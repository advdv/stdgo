package stdtxpgxv5_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdtx"
	"github.com/advdv/stdgo/stdtx/stdtxpgxv5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest"
	"go.uber.org/zap/zaptest/observer"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestSetup(t *testing.T) {
	ctx, drv, _ := setup(t)
	require.NotNil(t, drv)
	require.NotNil(t, ctx)
}

func TestSetupTx(t *testing.T) {
	ctx, drv, obs := setup(t,
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

	require.Len(t, obs.FilterMessage("exec").All(), 1)
	require.Len(t, obs.FilterMessage("query row").All(), 2)
}

func TestOnTxCommit(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		var called int64
		var accessMode pgx.TxAccessMode
		ctx, drv, _ := setup(t,
			stdtxpgxv5.AccessMode(pgx.ReadOnly),
			stdtxpgxv5.DiscourageSeqScan(true),
			stdtxpgxv5.OnTxCommit(func(ctx context.Context, am pgx.TxAccessMode, tx pgx.Tx) error {
				atomic.AddInt64(&called, 1)
				accessMode = am
				return nil
			}))

		tx, err := drv.BeginTx(ctx)
		require.NoError(t, err)
		require.NoError(t, drv.CommitTx(ctx, tx))
		require.Equal(t, int64(1), called)
		require.Equal(t, pgx.ReadOnly, accessMode)
	})

	t.Run("error", func(t *testing.T) {
		ctx, drv, _ := setup(t,
			stdtxpgxv5.DiscourageSeqScan(true),
			stdtxpgxv5.OnTxCommit(func(ctx context.Context, am pgx.TxAccessMode, tx pgx.Tx) error {
				return errors.New("foo")
			}))

		tx, err := drv.BeginTx(ctx)
		defer tx.Rollback(ctx)
		require.NoError(t, err)
		require.ErrorContains(t, drv.CommitTx(ctx, tx), "foo")
	})
}

func setup(tb testing.TB, opts ...stdtxpgxv5.Option) (
	context.Context,
	stdtx.Driver[pgx.Tx],
	*observer.ObservedLogs,
) {
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

	zc, obs := observer.New(zapcore.DebugLevel)
	tzc := zapcore.NewCore(zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig()),
		zaptest.NewTestingWriter(tb), zapcore.DebugLevel)
	ctx := stdctx.WithLogger(tb.Context(), zap.New(zapcore.NewTee(zc, tzc)))

	return ctx, stdtxpgxv5.New(db, opts...), obs
}
