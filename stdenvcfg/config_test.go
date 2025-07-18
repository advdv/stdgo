package stdenvcfg_test

import (
	"encoding/hex"
	"testing"

	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/stretchr/testify/assert"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

type Conf1 struct {
	Foo  string               `env:"FOO"`
	Bar  stdenvcfg.HexBytes   `env:"BAR"`
	Bars []stdenvcfg.HexBytes `env:"BARS"`
}

func TestConfigProviding(t *testing.T) {
	t.Setenv("FOO", "bar")
	t.Setenv("BAR", hex.EncodeToString([]byte("dar")))
	t.Setenv("BARS", hex.EncodeToString([]byte("1"))+","+hex.EncodeToString([]byte("2")))

	var cfg1 Conf1
	fxtest.New(t, fx.Populate(&cfg1), stdenvcfg.Provide[Conf1](), stdenvcfg.ProvideOSEnvironment())
	assert.Equal(t, "bar", cfg1.Foo)

	assert.Equal(t, "dar", string(cfg1.Bar))
	assert.Len(t, cfg1.Bars, 2)
	assert.Equal(t, "1", string(cfg1.Bars[0]))
	assert.Equal(t, "2", string(cfg1.Bars[1]))
}

func TestConfigProvidingPrefix(t *testing.T) {
	t.Setenv("FIX_FOO", "bar")

	var cfg1 Conf1
	fxtest.New(t, fx.Populate(&cfg1), stdenvcfg.Provide[Conf1]("FIX_"), stdenvcfg.ProvideOSEnvironment())
	assert.Equal(t, "bar", cfg1.Foo)
}

func TestConfigProvideNamed(t *testing.T) {
	t.Setenv("FIX_AB_FOO", "bar")
	t.Setenv("FIX_BB_FOO", "dar")

	var deps struct {
		fx.In
		CfgA Conf1 `name:"ab"`
		CfgB Conf1 `name:"bb"`
	}

	fxtest.New(t,
		fx.Populate(&deps),
		stdenvcfg.ProvideNamed[Conf1]("ab", "FIX_"),
		stdenvcfg.ProvideNamed[Conf1]("bb", "FIX_"),
		stdenvcfg.ProvideOSEnvironment(),
	)

	assert.Equal(t, "bar", deps.CfgA.Foo)
	assert.Equal(t, "dar", deps.CfgB.Foo)
}
