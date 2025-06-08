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
	require.ErrorContains(t, err, "plan cost exceeds maximum")
}
