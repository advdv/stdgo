package stdcrpcintercept

import (
	"context"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/advdv/stdgo/stdctx"
	"go.uber.org/zap"
)

// NewServerLogger returns the connect-go v2 interceptor that puts a logger for each RPC in the
// context, with fields that describe the RPC:
//
//   - "procedure", the RPC's procedure, such as "/acme.v1.ThingService/GetThing";
//   - "rpc.service" and "rpc.method", its parts, such as "acme.v1.ThingService" and "GetThing", named
//     as in OpenTelemetry's semantic conventions;
//   - "rpc.idempotency", its idempotency level, such as "no_side_effects" for a read;
//   - "rpc.protocol" and "rpc.codec", the wire protocol and codec of the call, such as "grpc" and
//     "proto", when the transport reports them.
//
// It builds on the logger already in the context, such as the request's logger that
// [github.com/advdv/stdgo/stdhttpware] puts there, and on logs for calls that come with none, such as
// in-process ones.
//
// stdtx, and so [github.com/advdv/stdgo/fx/stdpgxtxfx/stdpgxtxconnect.CTx], log to it, and panic
// without one. Install it outermost, so the interceptors inside it, such as [NewServerErrors], log to
// it too.
func NewServerLogger(logs *zap.Logger) connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			rpcLogs := logs
			if ctxLogs, ok := stdctx.Logger(ctx); ok {
				rpcLogs = ctxLogs
			}

			ctx = stdctx.WithLogger(ctx, rpcLogs.With(rpcFields(ctx, spec)...))

			return next(ctx, spec, stream)
		}
	}
}

// rpcFields returns the fields that [NewServerLogger] logs for the RPC of spec, called with ctx.
func rpcFields(ctx context.Context, spec connect.Spec) []zap.Field {
	fields := []zap.Field{
		zap.String("procedure", spec.Procedure),
		zap.Stringer("rpc.idempotency", spec.IdempotencyLevel),
	}

	// A procedure is "/<service>/<method>".
	if service, method, ok := strings.Cut(strings.TrimPrefix(spec.Procedure, "/"), "/"); ok {
		fields = append(fields, zap.String("rpc.service", service), zap.String("rpc.method", method))
	}

	if info, ok := connect.CallInfoForServerContext(ctx); ok {
		if info.Protocol != "" {
			fields = append(fields, zap.String("rpc.protocol", info.Protocol))
		}

		if info.Codec != "" {
			fields = append(fields, zap.String("rpc.codec", info.Codec))
		}
	}

	return fields
}
