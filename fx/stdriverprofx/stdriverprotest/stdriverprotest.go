// Package stdriverprotest waits, in tests, for the workflows that River Pro's
// clients (package stdriverprofx) work, as package stdrivertest does for jobs.
package stdriverprotest

import (
	"fmt"
	"slices"
	"testing"

	"github.com/advdv/stdgo/fx/stdriverfx/v2/stdrivertest"
	"github.com/advdv/stdgo/fx/stdriverprofx"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"riverqueue.com/riverpro"
)

// Workflow waits until every task of the workflow with id is finalized. It
// returns the tasks as client reads them.
func Workflow(tb testing.TB, client *stdriverprofx.Client, id string) *riverpro.WorkflowTasks {
	tb.Helper()

	var tasks *riverpro.WorkflowTasks

	stdrivertest.Eventually(tb, func(c *assert.CollectT) {
		workflow, err := client.WorkflowFromExistingID(tb.Context(), id, nil)
		require.NoError(c, err)

		got, err := workflow.LoadAll(tb.Context(), nil)
		require.NoError(c, err)

		for _, name := range got.Names() {
			job := got.Get(name).Job
			require.NotNil(c, job.FinalizedAt, "task %q is %s", name, job.State)
		}

		tasks = got
	})

	return tasks
}

// RequireWorkflowCompleted waits until every task of the workflow with id is
// finalized, as [Workflow] does, and requires that they all completed.
// Otherwise it fails with each task that did not, its state, and the errors of
// its attempts. It returns the tasks as client reads them.
func RequireWorkflowCompleted(tb testing.TB, client *stdriverprofx.Client, id string) *riverpro.WorkflowTasks {
	tb.Helper()

	tasks := Workflow(tb, client, id)

	var incomplete []string

	for _, name := range slices.Sorted(slices.Values(tasks.Names())) {
		if job := tasks.Get(name).Job; job.State != rivertype.JobStateCompleted {
			incomplete = append(incomplete, fmt.Sprintf("task %q: %s", name, stdrivertest.Describe(job)))
		}
	}

	require.Empty(tb, incomplete, "workflow %s has tasks that did not complete", id)

	return tasks
}
