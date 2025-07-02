package stdfx_test

import (
	"testing"

	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/advdv/stdgo/stdfx"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"
)

type Config struct {
	Foo string `env:"FOO"`
}

type Bar struct{}

type Params struct {
	fx.In
	LC fx.Lifecycle
	SD fx.Shutdowner

	Cfg  Config
	Logs *zap.Logger
}

type Result struct {
	fx.Out
	Bar Bar
}

func New(Params) (Result, error) { return Result{Bar: Bar{}}, nil }

var Module1 = stdfx.ZapEnvCfgModule[Config]("foo", New)

var Module2 = stdfx.NamedNoProvideZapEnvCfgModule[Config]("foo", "a", fx.Provide(New))

func TestZapCfgModule(t *testing.T) {
	var bar Bar
	app := fxtest.New(t, Module1, fx.Provide(zap.NewExample), fx.Populate(&bar), stdenvcfg.ProvideOSEnvironment())
	app.RequireStart()
	app.RequireStop()
}

func TestNamedZapCfgModule(t *testing.T) {
	var deps struct {
		fx.In
		Cfg Config `name:"a"`
	}
	app := fxtest.New(t, Module2, fx.Provide(zap.NewExample), fx.Populate(&deps), stdenvcfg.ProvideOSEnvironment())
	app.RequireStart()
	app.RequireStop()
}
