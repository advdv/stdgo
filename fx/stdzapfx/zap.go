// Package stdzapfx provides an opionated zap logger as an fx dependency.
package stdzapfx

import (
	"context"
	"fmt"

	"github.com/advdv/stdgo/stdfx"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Config configures the package.
type Config struct {
	// Level configures the minium logging level that will be captured.
	Level zapcore.Level `env:"LEVEL" envDefault:"info"`
	// Configure the level at which fx logs are shown, default to debug
	FxLevel zapcore.Level `env:"FX_LEVEL" envDefault:"debug"`
	// Outputs configures the zap outputs that will be opened for logging.
	Outputs []string `env:"OUTPUTS" envDefault:"stderr"`
	// Enables console encoding for more developer friendly logging output
	ConsoleEncoding bool `env:"CONSOLE_ENCODING"`
	// DevelopmentEncodingConfig enables encoding useful for developers.
	DevelopmentEncodingConfig bool `env:"DEVELOPMENT_ENCODING_CONFIG"`
}

// Fx is a convenient option that configures fx to use the zap logger.
func Fx() fx.Option {
	return fx.WithLogger(func(l *zap.Logger, cfg Config) fxevent.Logger {
		zl := &fxevent.ZapLogger{Logger: l.Named("fx")}
		zl.UseLogLevel(cfg.FxLevel)

		return zl
	})
}

type (
	// Params defines dependencies for creating the module's main component.
	Params struct {
		fx.In
		fx.Lifecycle
		Core zapcore.Core
	}

	// Result defines the modules main components from our zap module.
	Result struct {
		fx.Out
		Logger *zap.Logger
	}
)

// New constructs the package's components.
func New(params Params) (Result, error) {
	res := Result{
		Logger: zap.New(params.Core),
	}

	params.Lifecycle.Append(fx.Hook{
		OnStop: func(context.Context) error {
			_ = res.Logger.Sync() // ignore to support TTY: https://github.com/uber-go/zap/issues/880

			return nil
		},
	})

	return res, nil
}

func newWriteSyncer(cfg Config) (zapcore.WriteSyncer, error) {
	sync, _, err := zap.Open(cfg.Outputs...)
	if err != nil {
		return nil, fmt.Errorf("failed to zap-open: %w", err)
	}

	return sync, nil
}

func newLevelEnabler(cfg Config) zapcore.LevelEnabler {
	return cfg.Level
}

// newEncoder constructs the encoder based on the encoder config and our env config.
func newEncoder(cfg Config, ecfg zapcore.EncoderConfig) zapcore.Encoder {
	if cfg.ConsoleEncoding {
		return zapcore.NewConsoleEncoder(ecfg)
	}

	return zapcore.NewJSONEncoder(ecfg)
}

// newEncoderConfig constructs the encoder configuration.
func newEncoderConfig(cfg Config) zapcore.EncoderConfig {
	if cfg.DevelopmentEncodingConfig {
		return zap.NewDevelopmentEncoderConfig()
	}

	return zap.NewProductionEncoderConfig()
}

// Provide provides the package's components as an fx module.
func Provide() fx.Option {
	return stdfx.ZapEnvCfgModule[Config]("stdzap", New,
		sharedProvide(),
		fx.Provide(fx.Private, zapcore.NewCore, newWriteSyncer),
	)
}

func sharedProvide() fx.Option {
	return fx.Options(
		fx.Provide(fx.Private,
			newLevelEnabler,
			newEncoder,
			newEncoderConfig,
		))
}
