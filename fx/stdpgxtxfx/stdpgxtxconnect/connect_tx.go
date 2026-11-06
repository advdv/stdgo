// Package stdpgxtxconnect runs connect-go v2 RPCs in a transaction from the transactors that
// [stdpgxtxfx] provides. It is the v2 counterpart of [stdpgxtxfx.CTx], which takes connect-go v1
// requests.
package stdpgxtxconnect

import (
	"context"
	"errors"

	"connectrpc.com/connect/v2"
	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdtx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

// PgErrorsToConnectError statically defines how database errors are mapped to connect errors. The
// database error is attached as the cause: visible to errors.Is and errors.As on the server, but never
// sent to the client.
var PgErrorsToConnectError = map[string]func(pgErr *pgconn.PgError, logs *zap.Logger) *connect.Error{
	"42501": func(pgErr *pgconn.PgError, logs *zap.Logger) *connect.Error {
		logs.Info("database permission denied", zap.Error(pgErr))
		return connect.NewError(connect.CodePermissionDenied, "database permission denied").WithCause(pgErr)
	},
}

// CTx runs fn in a transaction from txr and returns its response message. It is a shorthand for the
// most common scenario in our connect RPC code. The transaction commits when fn returns no error and
// rolls back otherwise. A *connect.Error that fn returns is returned as is, so it reaches the client;
// database errors listed in [PgErrorsToConnectError] are mapped, and other errors are returned as stdtx
// returns them. stdtx retries fn when the transaction fails to serialize, so fn must be safe to run more
// than once. The context must carry a logger, see [stdctx.WithLogger].
func CTx[I, O any](
	ctx context.Context,
	txr *stdtx.Transactor[pgx.Tx],
	req *I,
	fn func(context.Context, *zap.Logger, pgx.Tx, *I) (*O, error),
) (*O, error) {
	msg, err := stdtx.Transact1(ctx, txr,
		func(ctx context.Context, tx pgx.Tx) (*O, error) { return fn(ctx, stdctx.Log(ctx), tx, req) })
	if err != nil {
		return nil, txError(ctx, err)
	}

	return msg, nil
}

// txError maps the error of a transaction to the error CTx returns.
func txError(ctx context.Context, err error) error {
	if connectErr, ok := errors.AsType[*connect.Error](err); ok {
		return connectErr
	}

	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		if mappedFn, ok := PgErrorsToConnectError[pgErr.Code]; ok {
			return mappedFn(pgErr, stdctx.Log(ctx))
		}
	}

	return err
}
