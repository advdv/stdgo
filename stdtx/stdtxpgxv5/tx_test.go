package stdtxpgxv5_test

import (
	"testing"

	"github.com/advdv/stdgo/stdtx"
	"github.com/advdv/stdgo/stdtx/stdtxpgxv5"
	"github.com/stretchr/testify/require"
)

func TestNoBeginSQL(t *testing.T) {
	ctx, drv, _ := setup(t, stdtxpgxv5.TestForMaxQueryPlanCosts(1))
	tx, err := drv.BeginTx(ctx)
	require.NoError(t, err)

	defer tx.Rollback(ctx)
}

func TestAssertQueryPlanCosts(t *testing.T) {
	ctx, drv, _ := setup(t, stdtxpgxv5.TestForMaxQueryPlanCosts(0.000001))
	tx, err := drv.BeginTx(ctx)
	require.NoError(t, err)

	defer tx.Rollback(ctx)

	var v int
	err = tx.QueryRow(ctx, `SELECT 42`).Scan(&v)
	require.ErrorContains(t, err, "plan cost exceeds maximum")

	_, err = tx.Query(ctx, `SELECT 42`) //nolint:sqlclosecheck
	require.ErrorContains(t, err, "plan cost exceeds maximum")

	_, err = tx.Exec(ctx, `SELECT generate_series(1, 100)`)
	require.ErrorContains(t, err, "plan cost exceeds maximum")
}

func TestAssertQueryPlanCostsWithDiscourage(t *testing.T) {
	ctx, drv, _ := setup(t,
		stdtxpgxv5.DiscourageSeqScan(true),
		stdtxpgxv5.TestForMaxQueryPlanCosts(300))
	tx, err := drv.BeginTx(ctx)
	require.NoError(t, err)

	defer tx.Rollback(ctx)

	ddlCtx := stdtx.WithNoTestForMaxQueryPlanCosts(ctx)
	_, err = tx.Exec(ddlCtx, `CREATE TEMP TABLE tmp_numbers (id BIGINT) ON COMMIT DROP`)
	require.NoError(t, err)

	_, err = tx.Exec(ddlCtx, `INSERT INTO tmp_numbers SELECT generate_series(1, 10000)`)
	require.NoError(t, err)

	var c int
	err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM tmp_numbers`).Scan(&c)
	require.ErrorContains(t, err, `query plan uses a disabled Seq Scan on "tmp_numbers"`)
}

// A scan that the planner had to use although it is disabled fails the query
// wherever it is in the plan, whatever the plan costs, while an index lookup
// passes.
func TestAssertQueryPlanCostsWithDiscourageNested(t *testing.T) {
	ctx, drv, _ := setup(t,
		stdtxpgxv5.DiscourageSeqScan(true),
		stdtxpgxv5.TestForMaxQueryPlanCosts(1e15))
	tx, err := drv.BeginTx(ctx)
	require.NoError(t, err)

	defer tx.Rollback(ctx)

	ddlCtx := stdtx.WithNoTestForMaxQueryPlanCosts(ctx)
	for _, sql := range []string{
		`CREATE TEMP TABLE tmp_a (id BIGINT PRIMARY KEY) ON COMMIT DROP`,
		`CREATE TEMP TABLE tmp_b (id BIGINT PRIMARY KEY, a_id BIGINT) ON COMMIT DROP`,
		`INSERT INTO tmp_a SELECT generate_series(1, 10000)`,
		`INSERT INTO tmp_b SELECT g, g FROM generate_series(1, 10000) g`,
		`ANALYZE tmp_a, tmp_b`,
	} {
		_, err = tx.Exec(ddlCtx, sql)
		require.NoError(t, err)
	}

	// tmp_b has no index on a_id, so the join scans it below an index scan.
	_, err = tx.Exec(ctx, `SELECT tmp_a.id, tmp_b.id FROM tmp_a JOIN tmp_b ON tmp_b.a_id = tmp_a.id WHERE tmp_a.id = 5`)
	require.ErrorContains(t, err, `query plan uses a disabled Seq Scan on "tmp_b"`)

	_, err = tx.Exec(ctx, `SELECT id FROM tmp_b WHERE id = 5`)
	require.NoError(t, err)
}
