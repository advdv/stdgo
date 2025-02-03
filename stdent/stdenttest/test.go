// Package stdenttest is a utility for writing tests on ent transaction.
package stdenttest

import (
	"context"
	"errors"
	"testing"

	"github.com/advdv/stdgo/stdent"
	"github.com/jackc/pgx/v5"
)

// Test is a utility method for testing code run in a transaction.
func Test[T stdent.Tx](
	ctx context.Context,
	tb testing.TB,
	txr *stdent.Transactor[T],
	fnc func(ctx context.Context, tx T),
) {
	if err := stdent.Transact0(ctx, txr, func(ctx context.Context, tx T) error {
		fnc(ctx, tx)
		return nil
	}); err != nil && !errors.Is(err, pgx.ErrTxCommitRollback) {
		// in case we're testing errors the commit is reached but it will fail with an expected error of
		// pgx.ErrTxCommitRollback. Outside of testing the 'fn' would return an error to check so we don't
		// call commit but t doesn't offer a way for us to check if the test has failed.

		tb.Fatalf("transact: %v", err)
	}
}
