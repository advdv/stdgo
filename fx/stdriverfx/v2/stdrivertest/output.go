package stdrivertest

import (
	"encoding/json"
	"testing"

	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/require"
)

// Output decodes the output that job recorded into a T, such as the job's
// output struct. It fails when the job recorded none. It reads plain jobs and
// workflow tasks (WorkflowTaskWithJob.Job) alike.
func Output[T any](tb testing.TB, job *rivertype.JobRow) T {
	tb.Helper()

	raw := job.Output()
	require.NotNil(tb, raw, "%s, and recorded no output", Describe(job))

	var output T
	require.NoError(tb, json.Unmarshal(raw, &output), "decode output of job %d (%s)", job.ID, job.Kind)

	return output
}
