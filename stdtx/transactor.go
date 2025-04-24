// Package stdtx provides a standardized way to handle database transactions.
package stdtx

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/advdv/stdgo/stdctx"
	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/retrypolicy"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

// Transactor provides transactions. It can be passed to [Transact0] and [Transact1] to eaily run code
// transactionally. A driver can be implemented to support different postgres libraries.
type Transactor[TTX any] struct {
	drv Driver[TTX]
}

// NewTransactor inits a transactor given the driver.
func NewTransactor[TTX any](drv Driver[TTX]) *Transactor[TTX] {
	return &Transactor[TTX]{drv}
}

// Transact0 runs [Transact1] but without a value to return.
func Transact0[TTx any](
	ctx context.Context,
	txr *Transactor[TTx],
	fnc func(ctx context.Context, tx TTx) error,
) (err error) {
	_, err = Transact1(ctx, txr, func(ctx context.Context, tx TTx) (struct{}, error) {
		return struct{}{}, fnc(ctx, tx)
	})

	return
}

// ErrAlreadyInTransactionScope is returned when transacting has detected that somewhere up the context call
// chain a transaction was already started.
var ErrAlreadyInTransactionScope = errors.New("attempt to transact while transaction was already detected")

// Transact1 runs fnc in a transaction TTx derived from the provided transactor while returning one value of type U.
func Transact1[TTx, U any](
	ctx context.Context,
	txr *Transactor[TTx],
	fnc func(ctx context.Context, tx TTx) (U, error),
) (res U, err error) {
	logs := stdctx.Log(ctx)

	// If there is an attempt count in the context it means that up the call chain a transaction was already started.
	// We error in this case because it must be passed down as an argument instead of a new transaction being started.
	// We could re-use the transaction but that makes code hard to read.
	if _, inTx := attemptsFromContext(ctx); inTx {
		return res, fmt.Errorf("%w", ErrAlreadyInTransactionScope)
	}

	retry := retrypolicy.Builder[U]().
		HandleIf(func(_ U, err error) bool {
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) {
				return false
			}

			if slices.Contains(txr.drv.SerializationFailureCodes(), pgErr.Code) {
				logs.Info("retrying due to serialization failure", zap.String("code", pgErr.Code))
				return true // retry for this code
			}

			return false
		}).
		WithMaxRetries(txr.drv.SerializationFailureMaxRetries()).
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

			tx, err := txr.drv.BeginTx(ctx)
			if err != nil {
				return res, err
			}

			// NOTE: this defer rollback is necessary if a routine is exited with runtime.Goexit(). In that case no
			// recover is triggered, and no error is returned either. This is common in assertion libraries
			// such as Testify's require. This defer ensures that the tx is also rolled back when an assert inside the
			// transaction is performed.
			defer func() {
				if err := txr.drv.RollbackTx(ctx, tx); err != nil && !errors.Is(err, txr.drv.TxDoneError()) {
					logs.Debug("tx defer callback failure", zap.Error(err))
				}
			}()

			defer func() {
				if v := recover(); v != nil {
					logs.Info("recovered panic in tx, rolling back", zap.Any("recovered", v))
					txr.drv.RollbackTx(ctx, tx) //nolint:errcheck
					panic(v)
				}
			}()

			// ctx = ContextWithTx(ctx, tx)
			ctx = contextWithAttempts(ctx, exec.Attempts())

			if res, err = fnc(ctx, tx); err != nil {
				logs.Info("transaction handler failed, rolling back transaction", zap.Error(err))
				if rerr := txr.drv.RollbackTx(ctx, tx); rerr != nil {
					err = fmt.Errorf("%w: rollback transaction: %s", err, rerr.Error())
				}

				return res, err
			}

			if err := txr.drv.CommitTx(ctx, tx); err != nil {
				// In cases the fnc logic concludes the transaction by itself we don't consider that
				// an error since the job was done either way.
				if errors.Is(err, txr.drv.TxDoneError()) {
					return res, nil
				}

				return res, fmt.Errorf("commit transaction: %w", err)
			}

			return res, err
		})
}
