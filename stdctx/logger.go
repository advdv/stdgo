package stdctx

import (
	"context"

	"go.uber.org/zap"
)

// Log returns a logger from the context or panics if none is available.
func Log(ctx context.Context) *zap.Logger {
	v, ok := Logger(ctx)
	if !ok {
		panic("stdctx: no logger in context")
	}

	return v
}

// MaybeLog returns a logger from the context, or a nop logger if it's not present.
func MaybeLog(ctx context.Context) *zap.Logger {
	v, ok := Logger(ctx)
	if !ok {
		return zap.NewNop()
	}

	return v
}

// Logger requires the calling code to behave differently if the logger is not present.
func Logger(ctx context.Context) (*zap.Logger, bool) {
	v, ok := ctx.Value(ctxKey("logger")).(*zap.Logger)
	if !ok {
		return nil, false
	}

	return v, true
}

// WithLogger add a zap logger to the context.
func WithLogger(ctx context.Context, logs *zap.Logger) context.Context {
	return context.WithValue(ctx, ctxKey("logger"), logs)
}
