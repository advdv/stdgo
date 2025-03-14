// Package stdent provides re-usable code for using Ent.
package stdent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	entsql "entgo.io/ent/dialect/sql"
	"go.uber.org/zap"

	"github.com/advdv/stdgo/stdctx"
	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/retrypolicy"
	"github.com/jackc/pgx/v5/pgconn"
)

// Tx describes the constraints for an Ent transaction.
type Tx interface {
	Commit() error
	Rollback() error
}

// Client defines an Ent client that begins transactions of type T.
type Client[T Tx] interface {
	BeginTx(ctx context.Context, opts *entsql.TxOptions) (T, error)
}

// Transact0 runs [Transact1] but without a value to return.
func Transact0[T Tx](
	ctx context.Context,
	txr *Transactor[T],
	fnc func(ctx context.Context, tx T) error,
) (err error) {
	_, err = Transact1(ctx, txr, func(ctx context.Context, tx T) (struct{}, error) {
		return struct{}{}, fnc(ctx, tx)
	})

	return
}

// Transact1 runs fnc in a transaction T derived from the provided Ent client while returning one value of type U. The
// implementation is taken from the official docs: https://entgo.io/docs/transactions#best-practices. If the context
// already has a transaction, it runs it in that one.
func Transact1[T Tx, U any](
	ctx context.Context,
	txr *Transactor[T],
	fnc func(ctx context.Context, tx T) (U, error),
) (res U, err error) {
	logs := stdctx.Log(ctx)

	tx, ok := txFromContext[T](ctx)
	if ok {
		logs.Debug("tx already in context, re-using it")

		return fnc(ctx, tx) // context already has a Tx.
	}

	retry := retrypolicy.Builder[U]().
		HandleIf(func(_ U, err error) bool {
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) {
				return false
			}

			for _, code := range txr.opts.serializationFailureCodes {
				if code == pgErr.Code {
					logs.Info("retrying due to serialization failure", zap.String("code", code))
					return true // retry for this code
				}
			}

			return false
		}).
		WithMaxRetries(txr.opts.serializationFailureMaxRetries). // @TODO allow configuration of the max retries.
		Build()

	return failsafe.
		NewExecutor(retry).
		WithContext(ctx).
		GetWithExecution(func(exec failsafe.Execution[U]) (res U, err error) { //nolint:contextcheck
			ctx := exec.Context()

			if exec.IsFirstAttempt() {
				logs.Debug("executing transaction, first time")
			} else {
				logs.Info("re-executing transaction", zap.Int("attempt", exec.Attempts()))
			}

			txOpts := &sql.TxOptions{
				Isolation: txr.opts.isolationLevel,
				ReadOnly:  txr.opts.readOnly,
			}

			tx, err = txr.client.BeginTx(ctx, txOpts)
			if err != nil {
				return res, err
			}

			// NOTE: this defer rollback is necessary if a routine is exited with runtime.Goexit(). In that case no
			// recover is triggered, and no error is returned either. This is common in assertion libraries
			// such as Testify's require. This defer ensures that the tx is also rolled back in failed tests.
			defer func() {
				if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
					logs.Debug("tx's defer callback failure", zap.Error(err))
				}
			}()

			defer func() {
				if v := recover(); v != nil {
					logs.Info("recovered panic in tx, rolling back", zap.Any("recovered", v))
					tx.Rollback() //nolint:errcheck
					panic(v)
				}
			}()

			ctx = ContextWithTx(ctx, tx)
			ctx = ContextWithAttempts(ctx, exec.Attempts())

			if res, err = fnc(ctx, tx); err != nil {
				if rerr := tx.Rollback(); rerr != nil {
					err = fmt.Errorf("%w: rollback transaction: %s", err, rerr.Error())
				}

				return res, err
			}

			if err := tx.Commit(); err != nil {
				// In cases the fnc logic concludes the transaction by itself we don't consider that
				// an error since the job was done either way.
				if errors.Is(err, sql.ErrTxDone) {
					return res, nil
				}

				return res, fmt.Errorf("commit transaction: %w", err)
			}

			return res, err
		})
}
