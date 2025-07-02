package stdzapfx_test

import (
	"testing"

	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestTestingLogger(t *testing.T) {
	var logs *zap.Logger
	var obs *observer.ObservedLogs

	app := fxtest.New(t,
		stdzapfx.Fx(),
		stdzapfx.TestProvide(t),
		fx.Populate(&logs, &obs),
		stdenvcfg.ProvideOSEnvironment())
	app.RequireStart()
	logs.Info("foo")
	app.RequireStop()

	require.Equal(t, 1, obs.FilterMessage("foo").Len())
}
