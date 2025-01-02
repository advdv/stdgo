package stdzapfx

import (
	"github.com/advdv/stdgo/stdfx"
	"go.uber.org/fx"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest"
	"go.uber.org/zap/zaptest/observer"
)

// newTestingCore creates a core we can assert with the observer core. While also writing logs
// to the testing writer. This allows logs to still be visible while testing with -v enabled.
func newTestingCore(
	enc zapcore.Encoder, ws zapcore.WriteSyncer, enab zapcore.LevelEnabler,
) (zapcore.Core, *observer.ObservedLogs) {
	core, obs := observer.New(enab)
	core = zapcore.NewTee(core, zapcore.NewCore(enc, ws, enab))

	return core, obs
}

// TestProvide provides the package's components as an fx module with a configuration for testing.
func TestProvide(tb zaptest.TestingT) fx.Option {
	return stdfx.ZapEnvCfgModule[Config]("stdzap", New,
		sharedProvide(),
		fx.Provide(fx.Private, func() zapcore.WriteSyncer { return zaptest.NewTestingWriter(tb) }),
		fx.Provide(newTestingCore),
	)
}
