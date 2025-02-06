package stdctx_test

import (
	"context"
	"testing"

	"github.com/advdv/stdgo/stdctx"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestLog_WithLogger(t *testing.T) {
	logger := zap.NewExample()
	ctx := context.Background()
	ctxWithLogger := stdctx.WithLogger(ctx, logger)
	assert.Equal(t, logger, stdctx.Log(ctxWithLogger))
}

func TestLog_NoLoggerPanics(t *testing.T) {
	ctx := context.Background()
	assert.PanicsWithValue(t, "stdctx: no logger in context", func() {
		stdctx.Log(ctx)
	})
}

func TestLog_MaybeLogger(t *testing.T) {
	ctx := context.Background()
	assert.NotNil(t, stdctx.MaybeLog(ctx))
}

func TestWithLogger_OverridesExistingLogger(t *testing.T) {
	logger1 := zap.NewExample()
	logger2 := zap.NewExample()
	ctx := context.Background()
	ctxWithLogger1 := stdctx.WithLogger(ctx, logger1)
	ctxWithLogger2 := stdctx.WithLogger(ctxWithLogger1, logger2)
	assert.Equal(t, logger2, stdctx.Log(ctxWithLogger2))
}
