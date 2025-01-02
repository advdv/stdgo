package stdfx_test

import (
	"testing"

	"github.com/advdv/stdgo/stdfx"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"
)

type Config struct {
	Foo string `env:"FOO"`
}

type Bar struct{}

func New(c Config, l *zap.Logger) Bar { return Bar{} }

var Module = stdfx.ZapEnvCfgModule[Config]("foo", fx.Provide(New))

func TestZapCfgModule(t *testing.T) {
	var bar Bar
	app := fxtest.New(t, Module, fx.Provide(zap.NewExample), fx.Populate(&bar))
	app.RequireStart()
	app.RequireStop()
}
