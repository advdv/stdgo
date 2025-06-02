package stdriverfx

import (
	"context"
	"fmt"
	"time"

	"buf.build/go/protovalidate"
	"github.com/advdv/stdgo/stdtx"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

// TransactWorker can be embedded into river workers to make the work be run in a database transaction.
type TransactWorker[A JobArgs, O JobOutput] struct {
	txr       *stdtx.Transactor[pgx.Tx]
	validator protovalidate.Validator
	hdlr      func(_ context.Context, _ pgx.Tx, job *river.Job[A]) (O, error)
}

func NewTransactWorker[A JobArgs, O JobOutput](
	txr *stdtx.Transactor[pgx.Tx],
	val protovalidate.Validator,
	hdlr func(_ context.Context, _ pgx.Tx, job *river.Job[A]) (O, error),
) *TransactWorker[A, O] {
	return &TransactWorker[A, O]{
		txr:       txr,
		hdlr:      hdlr,
		validator: val,
	}
}

// Work implements the river.Worker interface.
func (w TransactWorker[A, O]) Work(ctx context.Context, job *river.Job[A]) error {
	return w.transact(ctx, job)
}

// Transact call the work function 'work' in a transaction while completing the job on that transaction. It is ideal
// for middleware-like level logic across working all tasks.
func (w TransactWorker[A, O]) transact(
	ctx context.Context,
	job *river.Job[A],
) error {
	if err := w.validator.Validate(job.Args); err != nil {
		return river.JobCancel(fmt.Errorf("validate job arguments: %w", err))
	}

	if err := stdtx.Transact0(ctx, w.txr, func(ctx context.Context, tx pgx.Tx) error {
		dl, ok := ctx.Deadline()
		if ok {
			txTimeout := time.Until(dl.Add(time.Second * 5)).Milliseconds() // add a grace period
			if _, err := tx.Exec(ctx, fmt.Sprintf(`
				SET SESSION idle_in_transaction_session_timeout = %d;
				SET SESSION transaction_timeout = %d;
			`, txTimeout, txTimeout)); err != nil {
				return fmt.Errorf("failed to set session idle timeout: %w", err)
			}
		}

		output, err := w.hdlr(ctx, tx, job)
		if err != nil {
			return fmt.Errorf("perform work: %w", err)
		}

		if err := w.validator.Validate(output); err != nil {
			return river.JobCancel(fmt.Errorf("validate job output: %w", err))
		}

		if err := river.RecordOutput(ctx, output); err != nil {
			return fmt.Errorf("record output: %w", err)
		}

		if _, err := river.JobCompleteTx[*riverpgxv5.Driver](ctx, tx, job); err != nil {
			return fmt.Errorf("job complete tx: %w", err)
		}

		return nil
	}); err != nil {
		return fmt.Errorf("work transact: %w", err)
	}

	return nil
}
