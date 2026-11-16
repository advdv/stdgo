package stdent_test

import (
	"context"
	"database/sql"
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

func TestTxRawSQLExec(t *testing.T) {
	ctx := setup1(t)
	tx := setupTx(t, ctx, 100)

	q, ok := tx.(interface {
		ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	})
	require.True(t, ok)
	require.NotNil(t, q)

	res, err := q.ExecContext(ctx, `DO $$ BEGIN END; $$;`)
	require.NoError(t, err)
	require.NotNil(t, res)
}

func TestTxRawSQLQuery(t *testing.T) {
	ctx := setup1(t)
	tx := setupTx(t, ctx, 100)

	q, ok := tx.(interface {
		QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	})
	require.True(t, ok)
	require.NotNil(t, q)

	res, err := q.QueryContext(ctx, `select 42`)
	require.NoError(t, err)

	defer res.Close()

	var v int

	require.True(t, res.Next())
	require.NoError(t, res.Scan(&v))
	require.NoError(t, res.Err())
	require.Equal(t, 42, v)

	r, ok := tx.(interface {
		StandardTx() *sql.Tx
	})

	require.True(t, ok)
	require.NotNil(t, r)
	require.NotNil(t, r.StandardTx())
}

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
	err := tx.Query(ctx, `SELECT 42`, []any{}, &rows)
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

// A scan that the planner had to use although it is disabled fails the query
// wherever it is in the plan, whatever the plan costs, while an index lookup
// passes.
func TestTxDisabledSeqScan(t *testing.T) {
	ctx := setup1(t)
	tx := setupTx(t, ctx, 1e15)

	ddlCtx := stdent.WithNoTestForMaxQueryPlanCosts(ctx)
	for _, query := range []string{
		`CREATE TEMP TABLE tmp_a (id BIGINT PRIMARY KEY) ON COMMIT DROP`,
		`CREATE TEMP TABLE tmp_b (id BIGINT PRIMARY KEY, a_id BIGINT) ON COMMIT DROP`,
		`INSERT INTO tmp_a SELECT generate_series(1, 10000)`,
		`INSERT INTO tmp_b SELECT g, g FROM generate_series(1, 10000) g`,
		`ANALYZE tmp_a, tmp_b`,
	} {
		require.NoError(t, tx.Exec(ddlCtx, query, []any{}, nil))
	}

	// tmp_b has no index on a_id, so the join scans it below an index scan.
	var rows entsql.Rows
	err := tx.Query(ctx, `SELECT tmp_a.id, tmp_b.id FROM tmp_a JOIN tmp_b ON tmp_b.a_id = tmp_a.id WHERE tmp_a.id = 5`,
		[]any{}, &rows)
	require.ErrorContains(t, err, `query plan uses a disabled Seq Scan on "tmp_b"`)

	require.NoError(t, tx.Query(ctx, `SELECT id FROM tmp_b WHERE id = 5`, []any{}, &rows))
	require.NoError(t, rows.Close())
}

func TestBeginHook(t *testing.T) {
	var called bool

	ctx := setup1(t)
	tx := setupTx(t, ctx, 1, stdent.BeginHook(func(
		ctx context.Context, sql *strings.Builder, tx entdialect.ExecQuerier,
	) (*strings.Builder, error) {
		called = true

		sql.WriteString("SET LOCAL auth.foo='bar';")

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
