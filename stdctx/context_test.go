package stdctx_test

import (
	"testing"

	"github.com/advdv/stdgo/stdctx"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestLog_WithLogger(t *testing.T) {
	logger := zap.NewExample()
	ctxWithLogger := stdctx.WithLogger(t.Context(), logger)
	assert.Equal(t, logger, stdctx.Log(ctxWithLogger))
}

func TestLog_NoLoggerPanics(t *testing.T) {
	assert.PanicsWithValue(t, "stdctx: no logger in context", func() {
		stdctx.Log(t.Context())
	})
}

func TestLog_MaybeLogger(t *testing.T) {
	assert.NotNil(t, stdctx.MaybeLog(t.Context()))
}

func TestWithLogger_OverridesExistingLogger(t *testing.T) {
	logger1 := zap.NewExample()
	logger2 := zap.NewExample()
	ctxWithLogger1 := stdctx.WithLogger(t.Context(), logger1)
	ctxWithLogger2 := stdctx.WithLogger(ctxWithLogger1, logger2)
	assert.Equal(t, logger2, stdctx.Log(ctxWithLogger2))
}
