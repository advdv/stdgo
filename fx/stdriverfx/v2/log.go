package stdriverfx

import (
	"context"
	"io"

	"github.com/advdv/stdgo/stdctx"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverlog"
	"github.com/riverqueue/river/rivertype"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// logMiddleware gives every job attempt a logger in its context, which workers
// read with stdctx.Log, as RPC handlers do. Its lines go to logs, with the
// job's ID and kind, and River stores them with the attempt, which the River
// UI shows on the job's page.
func logMiddleware(logs *zap.Logger) []rivertype.Middleware {
	return []rivertype.Middleware{
		// Outermost: put logs, with the job's fields, in the context.
		river.WorkerMiddlewareFunc(func(
			ctx context.Context, job *rivertype.JobRow, doInner func(context.Context) error,
		) error {
			return doInner(stdctx.WithLogger(ctx, logs.With(
				zap.Int64("job_id", job.ID),
				zap.String("job_kind", job.Kind),
				zap.Int("job_attempt", job.Attempt),
			)))
		}),
		// Then also write that logger's lines, at its level, to the attempt's
		// log, which River stores. The job's fields are left out there: the
		// River UI shows the log on the job's page.
		riverlog.NewMiddlewareCustomContext(func(ctx context.Context, w io.Writer) context.Context {
			jobLogs := stdctx.Log(ctx)
			attempt := zapcore.NewCore(
				zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig()),
				zapcore.Lock(zapcore.AddSync(w)),
				jobLogs.Level())

			return stdctx.WithLogger(ctx, zap.New(zapcore.NewTee(jobLogs.Core(), attempt)))
		}, nil),
	}
}
