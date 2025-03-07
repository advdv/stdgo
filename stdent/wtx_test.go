package stdent_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdent"
	"github.com/peterldowns/pgtestdb"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	entdialect "entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestTxWithinMaxCost(t *testing.T) {
	ctx := setup1(t)
	tx := setupTx(t, ctx, 100)

	var rows entsql.Rows

	require.NoError(t, tx.Query(ctx, `SELECT current_setting('ENABLE_SEQSCAN')`, []any{}, &rows))
	enableSeqScan, err := entsql.ScanString(rows)
	require.NoError(t, err)
	require.Equal(t, "off", enableSeqScan)
}

func TestTxExceedMaxCostsQuery(t *testing.T) {
	ctx := setup1(t)
	tx := setupTx(t, ctx, 0.00001)

	var rows entsql.Rows
	err := tx.Query(ctx, `SELECT current_setting('auth.user_id')`, []any{}, &rows)
	require.ErrorContains(t, err, "plan cost exceeds maximum")
}

func TestTxExceedMaxCostsExec(t *testing.T) {
	ctx := setup1(t)
	tx := setupTx(t, ctx, 0.00001)

	var rows entsql.Rows
	err := tx.Query(ctx, `SELECT 42`, []any{}, &rows)
	require.ErrorContains(t, err, "plan cost exceeds maximum")
}

func TestTxExceedMaxCostsExecDisabled(t *testing.T) {
	ctx := setup1(t)
	tx := setupTx(t, ctx, 0.00001)
	ctx = stdent.WithNoTestForMaxQueryPlanCosts(ctx)

	var rows entsql.Rows
	err := tx.Query(ctx, `SELECT 42`, []any{}, &rows)
	require.NoError(t, err)
	require.NoError(t, rows.Close())
}

func TestBeginHook(t *testing.T) {
	var called bool
	ctx := setup1(t)
	tx := setupTx(t, ctx, 1, stdent.BeginHook(func(
		ctx context.Context, sql strings.Builder, tx stdent.Tx,
	) (strings.Builder, error) {
		called = true
		return sql, nil
	}))

	var rows entsql.Rows
	err := tx.Query(ctx, `SELECT 42`, []any{}, &rows)
	require.NoError(t, err)
	require.NoError(t, rows.Close())
	require.True(t, called)
}

func setup1(tb testing.TB, timeout ...time.Duration) context.Context {
	var ctx context.Context
	var cancel func()
	if len(timeout) > 0 {
		ctx, cancel = context.WithTimeout(tb.Context(), timeout[0])
	} else {
		ctx, cancel = context.WithCancel(tb.Context())
	}

	tb.Cleanup(cancel)

	ctx = stdctx.WithLogger(ctx, zap.NewNop())

	return ctx
}

func setupTx(t *testing.T, ctx context.Context, maxCost float64, opts ...stdent.DriverOption) entdialect.Tx {
	t.Helper()

	db := pgtestdb.New(t, pgtestdb.Config{
		DriverName: "pgx",
		User:       "postgres",
		Password:   "postgres",
		Database:   "postgres",
		Host:       "localhost",
		Port:       "5440",
	}, pgtestdb.NoopMigrator{})

	opts = append(opts, stdent.DiscourageSequentialScans(),
		stdent.TestForMaxQueryPlanCosts(maxCost))

	baseDrv := entsql.NewDriver(entdialect.Postgres, entsql.Conn{ExecQuerier: db})
	saasDrv := stdent.NewDriver(baseDrv, opts...)

	tx, err := saasDrv.Tx(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { tx.Rollback() })

	return tx
}
