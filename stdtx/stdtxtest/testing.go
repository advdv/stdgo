// Package stdtxtest provides testing utilities for our standard transactions.
package stdtxtest

import (
	"context"
	"testing"

	"github.com/advdv/stdgo/stdtx"
	"github.com/stretchr/testify/require"
)

// Transact test helper function that passes T to the function being executed in the tx.
func Transact[T testing.TB, TTx any](
	ctx context.Context,
	tb T,
	txr *stdtx.Transactor[TTx],
	fnc func(ctx context.Context, tb T, tx TTx) error,
) {
	require.NoError(tb, stdtx.Transact0(ctx, txr, func(ctx context.Context, tx TTx) error {
		return fnc(ctx, tb, tx)
	}))
}

// TransactNC is like [Transact] but disabes query plan cost checking.
func TransactNC[T testing.TB, TTx any](
	ctx context.Context,
	tb T,
	txr *stdtx.Transactor[TTx],
	fnc func(ctx context.Context, tb T, tx TTx) error,
) {
	ctx = stdtx.WithNoTestForMaxQueryPlanCosts(ctx) // assume we don't care about costs while asserting
	Transact(ctx, tb, txr, fnc)
}
