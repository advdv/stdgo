package stdriverfx

import (
	"context"
	"io"
	"log/slog"

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

// Middleware specific to our worker setup.
type Middleware struct {
	rivertype.WorkerMiddleware
}

func NewMiddleware() *Middleware {
	return &Middleware{}
}

// Work implements our middleware such that we can provide workers with a zap.Logger instead of a slog Logger.
func (mw *Middleware) Work(ctx context.Context, job *rivertype.JobRow, doInner func(ctx context.Context) error) error {
	var logw io.Writer
	inner := riverlog.NewMiddleware(func(w io.Writer) slog.Handler {
		logw = w // keep it so we can use for logging our own lines.
		return slog.DiscardHandler
	}, nil)

	return inner.Work(ctx, job, func(ctx context.Context) error {
		// turn the per-job io.Writer into a sync writer for a new zap core.
		workSyncWriter := zapcore.Lock(zapcore.AddSync(logw))

		// configure the new core, what is stored in the db should be human readable.
		workCore := zapcore.NewCore(zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig()),
			workSyncWriter,
			zap.DebugLevel)

		// our per-job logger will write to the per-job core, and the global core.
		logs := zap.New(zapcore.NewTee(workCore, stdctx.Log(ctx).Core()))
		defer logs.Sync() //nolint:errcheck
		ctx = context.WithValue(ctx, ctxKey("logger"), logs)

		return doInner(ctx)
	})
}
