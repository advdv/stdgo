// Package stdpgxtxconnect runs connect-go v2 RPCs in a transaction from the transactors that
// [stdpgxtxfx] provides. It is the v2 counterpart of [stdpgxtxfx.CTx], which takes connect-go v1
// requests.
package stdpgxtxconnect

import (
	"context"
	"errors"

	"connectrpc.com/connect/v2"
	"github.com/advdv/stdgo/stdcrpc/stdcrpcintercept"
	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdtx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

// CTx runs fn in a transaction from txr and returns its response message. It is a shorthand for the
// most common scenario in our connect RPC code. The transaction commits when fn returns no error and
// rolls back otherwise. stdtx retries fn when the transaction fails to serialize, so fn must be safe to
// run more than once. The context must carry a logger, see [stdctx.WithLogger].
//
// CTx maps the error of the transaction with the first rule that applies:
//
//   - a *connect.Error that fn returns is returned as is, so it reaches the client;
//   - each of mappers in turn, until one returns an error, as [stdcrpcintercept.NewServerErrors] runs
//     them; pass them to map the errors of this RPC only, such as a unique violation to the
//     ALREADY_EXISTS of the resource it creates, and to the server interceptor for errors that recur
//     across RPCs;
//   - a serialization failure or deadlock (SQLSTATE 40001 or 40P01) that remains after stdtx ran out of
//     retries becomes ABORTED, the code for a concurrency conflict that the client may retry;
//   - other errors are returned as stdtx returns them, for the server's error interceptor to map.
func CTx[I, O any](
	ctx context.Context,
	txr *stdtx.Transactor[pgx.Tx],
	req *I,
	fn func(context.Context, *zap.Logger, pgx.Tx, *I) (*O, error),
	mappers ...stdcrpcintercept.ErrorMapper,
) (*O, error) {
	msg, err := stdtx.Transact1(ctx, txr,
		func(ctx context.Context, tx pgx.Tx) (*O, error) { return fn(ctx, stdctx.Log(ctx), tx, req) })
	if err != nil {
		return nil, txError(ctx, err, mappers)
	}

	return msg, nil
}

// txError maps the error of a transaction to the error CTx returns, with the rules CTx lists.
func txError(ctx context.Context, err error, mappers []stdcrpcintercept.ErrorMapper) error {
	if connectErr, ok := errors.AsType[*connect.Error](err); ok {
		return connectErr
	}

	for _, mapper := range mappers {
		if connectErr := mapper(ctx, stdctx.Log(ctx), err); connectErr != nil {
			return connectErr
		}
	}

	// The codes stdtx retries, see stdtxpgxv5: serialization_failure and deadlock_detected.
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && (pgErr.Code == "40001" || pgErr.Code == "40P01") {
		stdctx.Log(ctx).Info("transaction aborted after retries", zap.Error(pgErr))

		// The database error is the cause: visible to errors.Is and errors.As on the server, but never
		// sent to the client.
		return connect.NewError(connect.CodeAborted, "transaction aborted by a concurrent one, retry").WithCause(pgErr)
	}

	return err
}
