package stdriverprofx_test

import (
	"testing"

	"github.com/advdv/stdgo/fx/stdriverprofx"
	"github.com/advdv/stdgo/fx/stdriverprofx/stdriverprotest"
	"github.com/cockroachdb/errors"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/stretchr/testify/require"
	"riverqueue.com/riverpro"
	"riverqueue.com/riverpro/riverworkflow"
)

// newEchoWorkflow builds a workflow of two echo tasks, the second depending on
// the first.
func newEchoWorkflow(client *stdriverprofx.Client) *riverpro.WorkflowT[pgx.Tx] {
	workflow := client.NewWorkflow(&riverpro.WorkflowOpts{Name: "echo"})
	workflow.Add("first", echoArgs{Message: "first"}, nil, nil)
	workflow.Add("second", echoArgs{Message: "second"}, nil, &riverpro.WorkflowTaskOpts{Deps: []string{"first"}})

	return workflow
}

// The workflow's tasks commit with the transaction, under the ID it returns.
func TestInsertWorkflowManyTx(t *testing.T) {
	t.Parallel()

	d := setup(t)
	workflow := newEchoWorkflow(d.RW)

	var id string

	require.NoError(t, pgx.BeginFunc(t.Context(), d.Pool, func(tx pgx.Tx) error {
		var err error
		id, err = stdriverprofx.InsertWorkflowManyTx(t.Context(), tx, d.RW, workflow)

		return err
	}))
	require.Equal(t, workflow.ID(), id)

	tasks := stdriverprotest.RequireWorkflowCompleted(t, d.RO, id)
	require.ElementsMatch(t, []string{"first", "second"}, tasks.Names())
}

// The workflow's tasks roll back with the transaction.
func TestInsertWorkflowManyTxRollsBack(t *testing.T) {
	t.Parallel()

	d := setup(t)
	workflow := newEchoWorkflow(d.RW)

	errRollback := errors.New("roll back")
	err := pgx.BeginFunc(t.Context(), d.Pool, func(tx pgx.Tx) error {
		if _, err := stdriverprofx.InsertWorkflowManyTx(t.Context(), tx, d.RW, workflow); err != nil {
			return err
		}

		return errRollback
	})
	require.ErrorIs(t, err, errRollback)

	params, err := riverworkflow.JobListParamsByID(workflow.ID(), river.NewJobListParams())
	require.NoError(t, err)

	jobs, err := d.RO.JobList(t.Context(), params)
	require.NoError(t, err)
	require.Empty(t, jobs.Jobs)
}
