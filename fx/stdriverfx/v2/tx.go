package stdriverfx

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

// CompleteTx runs fn in a transaction on pool, and completes job in that same
// transaction, so the job's writes and its completion commit together, or
// neither does. Workers that write to the database return its result from
// Work.
//
// It works for clients of either edition: River completes the job with the
// driver of the client in ctx, the one that works the job. The driver type
// passed to river.JobCompleteTx only names the transaction's type.
func CompleteTx[T river.JobArgs](
	ctx context.Context, pool *pgxpool.Pool, job *river.Job[T], fn func(context.Context, pgx.Tx) error,
) error {
	return errors.Wrap(pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if err := fn(ctx, tx); err != nil {
			return err
		}

		_, err := river.JobCompleteTx[*riverpgxv5.Driver](ctx, tx, job)

		return errors.Wrap(err, "complete job")
	}), "complete job in transaction")
}
