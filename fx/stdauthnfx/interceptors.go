package stdauthnfx

import (
	"context"

	"connectrpc.com/connect"
	"github.com/advdv/stdgo/stdctx"
	"github.com/cockroachdb/errors"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func (ac *AccessControl) GRPCInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		next grpc.UnaryHandler,
	) (resp any, err error) {
		var authzValue string
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if vals := md.Get("authorization"); len(vals) > 0 {
				authzValue = vals[0]
			}
		}

		if ctx, err = ac.Authenticate(ctx, authzValue); err != nil {
			stdctx.Log(ctx).Info("failed to authenticate GRPC", zap.Error(err))
			return nil, status.Error(codes.Unauthenticated, "failed to authenticate")
		}

		return next(ctx, req)
	}
}

func (ac *AccessControl) CRPCInterceptor() connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (_ connect.AnyResponse, err error) {
			if ctx, err = ac.Authenticate(ctx, req.Header().Get("Authorization")); err != nil {
				stdctx.Log(ctx).Info("failed to authenticate CRPC", zap.Error(err))
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("failed to authenticate"))
			}

			return next(ctx, req)
		}
	})
}
