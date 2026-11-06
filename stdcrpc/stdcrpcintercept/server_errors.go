package stdcrpcintercept

import (
	"context"
	"errors"

	"connectrpc.com/connect/v2"
	"github.com/advdv/stdgo/stdctx"
	"go.uber.org/zap"
)

// ErrorMapper maps the errors of an RPC that it knows to the Connect error the client receives, such
// as a domain error to ALREADY_EXISTS. It returns nil for the errors it leaves to the next mapper, or
// to the default mapping of [NewServerErrors]. It logs to logs what it keeps from the client.
type ErrorMapper func(ctx context.Context, logs *zap.Logger, err error) *connect.Error

// NewServerErrors returns the interceptor that turns every error of the RPCs it wraps into the
// Connect error the client receives. Handlers return a *connect.Error for the outcomes a client
// should see, such as NOT_FOUND, and wrap anything else. The interceptor maps an error with the first
// rule that applies:
//
//   - a *connect.Error in the error's chain is the handler's own verdict, and is returned as is,
//     unwrapped from the rest of the chain; a remote one is another server's verdict, from a client
//     call the handler made, and is not passed on;
//   - each of mappers in turn, until one returns an error;
//   - context.Canceled and context.DeadlineExceeded become CANCELED and DEADLINE_EXCEEDED;
//   - anything else is logged and becomes INTERNAL, with a message that does not reveal it.
//
// The default rules keep the error as the cause, for errors.Is and errors.As on the server; Connect
// never sends the cause to the client. The interceptor complements
// [github.com/advdv/stdgo/fx/stdpgxtxfx/stdpgxtxconnect.CTx], which maps the database errors it knows
// inside a transaction, and returns other errors as they are.
//
// It logs, and gives mappers, the logger in the context, or logs when the context has none. Install
// it outside the interceptors whose errors it should map, such as the one that validates requests.
func NewServerErrors(logs *zap.Logger, mappers ...ErrorMapper) connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			err := next(ctx, spec, stream)
			if err == nil {
				return nil
			}

			rpcLogs := logs
			if ctxLogs, ok := stdctx.Logger(ctx); ok {
				rpcLogs = ctxLogs
			}

			return toConnect(ctx, rpcLogs, err, mappers)
		}
	}
}

// toConnect maps err with the rules that [NewServerErrors] lists.
func toConnect(ctx context.Context, logs *zap.Logger, err error, mappers []ErrorMapper) *connect.Error {
	if connectErr, ok := errors.AsType[*connect.Error](err); ok && !connectErr.IsRemote() {
		return connectErr
	}

	for _, mapper := range mappers {
		if connectErr := mapper(ctx, logs, err); connectErr != nil {
			return connectErr
		}
	}

	switch {
	case errors.Is(err, context.Canceled):
		return connect.NewError(connect.CodeCanceled, "canceled").WithCause(err)
	case errors.Is(err, context.DeadlineExceeded):
		return connect.NewError(connect.CodeDeadlineExceeded, "deadline exceeded").WithCause(err)
	}

	logs.Error("rpc failed", zap.Error(err))

	return connect.NewError(connect.CodeInternal, "internal error").WithCause(err)
}
