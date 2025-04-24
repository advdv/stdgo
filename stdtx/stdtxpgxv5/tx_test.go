package stdtxpgxv5_test

import (
	"testing"

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
