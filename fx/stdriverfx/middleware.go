package stdriverfx

import (
	"context"
	"io"

	"github.com/advdv/stdgo/stdctx"
	"github.com/riverqueue/river/riverlog"
	"github.com/riverqueue/river/rivertype"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type ctxKey string

func Log(ctx context.Context) *zap.Logger {
	logs, ok := ctx.Value(ctxKey("logger")).(*zap.Logger)
	if !ok {
		panic("work: no work logger context")
	}

	return logs
}

// middleware for logging with a Zap logger.
func loggerMiddleware() rivertype.Middleware {
	return riverlog.NewMiddlewareCustomContext(func(ctx context.Context, w io.Writer) context.Context {
		workSyncWriter := zapcore.Lock(zapcore.AddSync(w))
		workCore := zapcore.NewCore(zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig()),
			workSyncWriter,
			zap.DebugLevel)
		logs := zap.New(zapcore.NewTee(workCore, stdctx.Log(ctx).Core()))
		return context.WithValue(ctx, ctxKey("logger"), logs)
	}, nil)
}
