// Package stdrivertest provides test utilities for our River abstraction.
package stdrivertest

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/advdv/stdgo/fx/stdriverfx"
	"github.com/advdv/stdgo/stdtx"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func JobInState(expectState ...rivertype.JobState) func(jr *rivertype.JobRow) bool {
	return func(jr *rivertype.JobRow) bool {
		return slices.Contains(expectState, jr.State)
	}
}

// WaitForJob will wait for a job to be in one of the provided states.
func WaitForJob(
	ctx context.Context,
	tb testing.TB,
	txr *stdtx.Transactor[pgx.Tx],
	wrk interface {
		GetJobByKinds(
			ctx context.Context, tx pgx.Tx, kind string, moreKinds ...string,
		) (*river.JobListResult, error)
	},
	args stdriverfx.JobArgs,
	expectN int,
	stateFn func(job *rivertype.JobRow) bool,
) (jobs []*rivertype.JobRow) {
	tb.Helper()
	require.EventuallyWithT(tb, func(tb *assert.CollectT) {
		require.NoError(tb, stdtx.Transact0(ctx, txr, func(ctx context.Context, tx pgx.Tx) error {
			res, err := wrk.GetJobByKinds(ctx, tx, args.Kind())
			require.NoError(tb, err)
			require.Len(tb, res.Jobs, expectN)
			for _, job := range res.Jobs {
				require.True(tb, stateFn(job), "no job every reached the required shape")
			}

			jobs = res.Jobs
			return nil
		}))
	}, time.Second*3, time.Millisecond*10)

	return jobs
}

// EnqueueJob will enqueue a job for testing.
func EnqueueJob[T stdriverfx.JobArgs](
	ctx context.Context,
	tb testing.TB,
	txr *stdtx.Transactor[pgx.Tx],
	enq stdriverfx.Enqueuer[T],
	args T,
) {
	tb.Helper()
	require.NoError(tb, stdtx.Transact0(ctx, txr, func(ctx context.Context, tx pgx.Tx) error {
		return enq.Enqueue(ctx, tx, args)
	}))
}
