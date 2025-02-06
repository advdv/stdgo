package stdentsaas_test

import (
	"context"
	"testing"
	"time"

	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdentsaas"
	"github.com/peterldowns/pgtestdb"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	entdialect "entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestTxWithinMaxCost(t *testing.T) {
	ctx := setup(t)
	ctx = stdentsaas.WithAuthenticatedUser(ctx, "a2a0a29c-dbc1-4d0b-b379-afa2af5ab00f")
	tx := setupTx(t, ctx, 100)

	var rows entsql.Rows
	require.NoError(t, tx.Query(ctx, `SELECT current_setting('auth.user_id')`, []any{}, &rows))
	currentUserID, err := entsql.ScanString(rows)
	require.NoError(t, err)
	require.Equal(t, "a2a0a29c-dbc1-4d0b-b379-afa2af5ab00f", currentUserID)

	require.NoError(t, tx.Query(ctx, `SELECT current_setting('ENABLE_SEQSCAN')`, []any{}, &rows))
	enableSeqScan, err := entsql.ScanString(rows)
	require.NoError(t, err)
	require.Equal(t, "off", enableSeqScan)
}

func TestTxExceedMaxCostsQuery(t *testing.T) {
	ctx := setup(t)
	tx := setupTx(t, ctx, 0.00001)

	var rows entsql.Rows
	err := tx.Query(ctx, `SELECT current_setting('auth.user_id')`, []any{}, &rows)
	require.ErrorContains(t, err, "plan cost exceeds maximum")
}

func TestTxExceedMaxCostsExec(t *testing.T) {
	ctx := setup(t)
	tx := setupTx(t, ctx, 0.00001)

	var rows entsql.Rows
	err := tx.Query(ctx, `SELECT 42`, []any{}, &rows)
	require.ErrorContains(t, err, "plan cost exceeds maximum")
}

func TestTxExceedMaxCostsExecDisabled(t *testing.T) {
	ctx := setup(t)
	tx := setupTx(t, ctx, 0.00001)
	ctx = stdentsaas.WithNoTestForMaxQueryPlanCosts(ctx)

	var rows entsql.Rows
	err := tx.Query(ctx, `SELECT 42`, []any{}, &rows)
	require.NoError(t, err)
	require.NoError(t, rows.Close())
}

func setup(tb testing.TB, timeout ...time.Duration) context.Context {
	var ctx context.Context
	var cancel func()
	if len(timeout) > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), timeout[0])
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}

	tb.Cleanup(cancel)

	ctx = stdctx.WithLogger(ctx, zap.NewNop())

	return ctx
}

func setupTx(t *testing.T, ctx context.Context, maxCost float64) entdialect.Tx {
	t.Helper()

	db := pgtestdb.New(t, pgtestdb.Config{
		DriverName: "pgx",
		User:       "postgres",
		Password:   "postgres",
		Database:   "postgres",
		Host:       "localhost",
		Port:       "5440",
	}, pgtestdb.NoopMigrator{})

	baseDrv := entsql.NewDriver(entdialect.Postgres, entsql.Conn{ExecQuerier: db})
	saasDrv := stdentsaas.NewDriver(baseDrv,
		stdentsaas.DiscourageSequentialScans(),
		stdentsaas.TestForMaxQueryPlanCosts(maxCost),
		stdentsaas.AuthenticatedUserSetting("auth.user_id"),
		stdentsaas.AuthenticatedOrganizationsSetting("auth.organizations"))

	tx, err := saasDrv.Tx(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { tx.Rollback() })

	return tx
}
