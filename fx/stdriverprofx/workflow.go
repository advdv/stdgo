package stdriverprofx

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/jackc/pgx/v5"
	"riverqueue.com/riverpro"
)

// InsertWorkflowManyTx validates workflow and inserts its tasks with client's
// InsertManyTx in tx, so the workflow commits or rolls back with the work that
// starts it. It returns the workflow's ID. Insert a workflow once: inserting it
// again inserts its tasks again.
func InsertWorkflowManyTx(
	ctx context.Context, tx pgx.Tx, client *Client, workflow *riverpro.WorkflowT[pgx.Tx],
) (string, error) {
	prepared, err := workflow.PrepareTx(ctx, tx)
	if err != nil {
		return "", errors.Wrapf(err, "prepare workflow %q", workflow.Name())
	}

	if _, err := client.InsertManyTx(ctx, tx, prepared.Jobs); err != nil {
		return "", errors.Wrapf(err, "insert workflow %q", workflow.Name())
	}

	return workflow.ID(), nil
}
