package stdzapfx

import (
	"context"

	"go.uber.org/zap"
)

type ctxKey string

// Log returns a logger from the context or panics if none is available.
func Log(ctx context.Context) *zap.Logger {
	logs, ok := ctx.Value(ctxKey("logger")).(*zap.Logger)
	if !ok {
		panic("stdzapfx: no zap logger in context")
	}

	return logs
}

// WithLogger add a zap logger to the context.
func WithLogger(ctx context.Context, logs *zap.Logger) context.Context {
	return context.WithValue(ctx, ctxKey("logger"), logs)
}
