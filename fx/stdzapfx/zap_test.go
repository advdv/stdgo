package stdzapfx_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"
)

func TestLogger(t *testing.T) {
	tmpfp := filepath.Join(t.TempDir(), fmt.Sprintf("test_logging_%d.log", time.Now().UnixNano()))
	var logs *zap.Logger

	app := fxtest.New(t,
		stdzapfx.Fx(),
		stdzapfx.Provide(),
		fx.Populate(&logs),
		fx.Decorate(func(c stdzapfx.Config) stdzapfx.Config {
			c.Outputs = []string{tmpfp}
			c.Level = zap.WarnLevel
			return c
		}),
	)

	app.RequireStart()
	logs.Info("some-info-message")
	logs.Warn("some-warn-message")

	app.RequireStop()
	require.NotNil(t, logs)

	data, err := os.ReadFile(tmpfp)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "some-info-message")
	assert.Contains(t, string(data), "some-warn-message")
}
