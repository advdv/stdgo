package stdzapfx_test

import (
	"context"
	"testing"

	"github.com/advdv/stdgo/fx/stdzapfx"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestLog_WithLogger(t *testing.T) {
	logger := zap.NewExample()
	ctx := context.Background()
	ctxWithLogger := stdzapfx.WithLogger(ctx, logger)
	assert.Equal(t, logger, stdzapfx.Log(ctxWithLogger))
}

func TestLog_NoLoggerPanics(t *testing.T) {
	ctx := context.Background()
	assert.PanicsWithValue(t, "stdzapfx: no zap logger in context", func() {
		stdzapfx.Log(ctx)
	})
}

func TestWithLogger_OverridesExistingLogger(t *testing.T) {
	logger1 := zap.NewExample()
	logger2 := zap.NewExample()
	ctx := context.Background()
	ctxWithLogger1 := stdzapfx.WithLogger(ctx, logger1)
	ctxWithLogger2 := stdzapfx.WithLogger(ctxWithLogger1, logger2)
	assert.Equal(t, logger2, stdzapfx.Log(ctxWithLogger2))
}
