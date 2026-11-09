package stdrivertest

import (
	"fmt"
	"strings"
	"testing"

	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/require"
)

// RequireJobCompleted waits until the job with id is finalized, as [Job]
// does, and requires that it completed. Otherwise it fails with the job's state
// and the errors of its attempts. It returns the job as client reads it.
func RequireJobCompleted(tb testing.TB, client JobGetter, id int64) *rivertype.JobRow {
	tb.Helper()

	job := Job(tb, client, id)
	require.Equal(tb, rivertype.JobStateCompleted, job.State, Describe(job))

	return job
}

// Describe the job's kind, state, and the errors of its attempts, for the
// message of a failed requirement.
func Describe(job *rivertype.JobRow) string {
	var b strings.Builder

	fmt.Fprintf(&b, "job %d (%s) is %s", job.ID, job.Kind, job.State)

	for _, attempt := range job.Errors {
		fmt.Fprintf(&b, "\n  attempt %d: %s", attempt.Attempt, attempt.Error)
	}

	return b.String()
}
