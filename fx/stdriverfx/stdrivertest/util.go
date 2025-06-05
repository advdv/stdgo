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
	"github.com/stretchr/testify/require"
)

func JobInState(expectState ...rivertype.JobState) func(jr *rivertype.JobRow) bool {
	return func(jr *rivertype.JobRow) bool {
		return slices.Contains(expectState, jr.State)
	}
}

// WaitForJobsByKind will wait for N jobs of a certain kind to be in one of the provided states.
func WaitForJobsByKind(
	ctx context.Context,
	tb testing.TB,
	txr *stdtx.Transactor[pgx.Tx],
	wrk interface {
		GetJobByKinds(
			ctx context.Context, tx pgx.Tx, kind string, moreKinds ...string,
		) (*river.JobListResult, error)
	},
	kind string,
	expN int,
	filterFn func(job *rivertype.JobRow) bool,
) (res []*rivertype.JobRow) {
	require.Eventually(tb, func() bool {
		jobs, err := stdtx.Transact1(ctx, txr, func(ctx context.Context, tx pgx.Tx) (*river.JobListResult, error) {
			return wrk.GetJobByKinds(ctx, tx, kind)
		})
		require.NoError(tb, err)

		// filter the rows we're interested in
		var filtered []*rivertype.JobRow
		for _, job := range jobs.Jobs {
			if filterFn(job) {
				filtered = append(filtered, job)
			}
		}

		// not (yet) the expected number of rows.
		if len(filtered) != expN {
			return false
		}

		res = filtered
		return true
	}, time.Second*3, time.Millisecond*10)

	return res
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
