// Package stdrivertest waits, in tests, for the jobs that River clients work,
// and reads what they recorded. Its helpers take clients of either edition of
// River, and [Eventually] and [Describe] let helpers for other River state,
// such as River Pro's workflows, wait and fail in the same way.
package stdrivertest

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// JobGetter reads a job by its ID, as the clients of either edition do.
type JobGetter interface {
	JobGet(ctx context.Context, id int64) (*rivertype.JobRow, error)
}

// pollInterval is how often Eventually checks its condition.
const pollInterval = 20 * time.Millisecond

// waitFor is how long Eventually waits: until shortly before the deadline of
// the test binary (go test -timeout), so it fails with its own message rather
// than the binary's timeout panic; without a deadline, indefinitely.
func waitFor(tb testing.TB) time.Duration {
	if t, ok := tb.(interface{ Deadline() (time.Time, bool) }); ok {
		if deadline, ok := t.Deadline(); ok {
			return time.Until(deadline) * 9 / 10
		}
	}

	return math.MaxInt64
}

// Eventually waits until condition passes, checking it again every few
// milliseconds, until shortly before the deadline of the test binary.
func Eventually(tb testing.TB, condition func(c *assert.CollectT)) {
	tb.Helper()

	require.EventuallyWithT(tb, condition, waitFor(tb), pollInterval)
}

// Job waits until the job with id is finalized: completed, cancelled, or
// discarded. It returns the job as client reads it.
func Job(tb testing.TB, client JobGetter, id int64) *rivertype.JobRow {
	tb.Helper()

	var job *rivertype.JobRow

	Eventually(tb, func(c *assert.CollectT) {
		got, err := client.JobGet(tb.Context(), id)
		require.NoError(c, err)
		require.NotNil(c, got.FinalizedAt, "job %d is %s", id, got.State)

		job = got
	})

	return job
}
