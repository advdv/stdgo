package stdpgxtxfx

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdtx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

// PgErrorsToConnectError statically defines how database errors are mapped to connect errors.
var PgErrorsToConnectError = map[string]func(lpgErr *pgconn.PgError, logs *zap.Logger) error{
	"42501": func(pgErr *pgconn.PgError, logs *zap.Logger) error {
		logs.Info("database permission denied", zap.Error(pgErr))
		return connect.NewError(connect.CodePermissionDenied, errors.New("database permission denied"))
	},
}

// CTx transacts while returning a protobuf message for the response. It is a shorthant for the most common
// scenario in our connect RPC code and makes it way more succinct.
func CTx[I, O any](
	ctx context.Context,
	txr *stdtx.Transactor[pgx.Tx],
	req *connect.Request[I],
	fn func(context.Context, *zap.Logger, pgx.Tx, *I) (*O, error),
) (*connect.Response[O], error) {
	m, err := stdtx.Transact1(ctx, txr,
		func(ctx context.Context, tx pgx.Tx) (*O, error) { return fn(ctx, stdctx.Log(ctx), tx, req.Msg) })
	return txResponse(ctx, m, err)
}

// txResponse creates consistent responses from request handling that runs in a transaction.
func txResponse[O any](ctx context.Context, msg *O, err error) (*connect.Response[O], error) {
	if err != nil {
		return nil, txError(ctx, err)
	}

	return connect.NewResponse(msg), nil
}

func txError(ctx context.Context, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		mappedFn, ok := PgErrorsToConnectError[pgErr.Code]
		if ok {
			return mappedFn(pgErr, stdctx.Log(ctx))
		}
	}
	return err
}
