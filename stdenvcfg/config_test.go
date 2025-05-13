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
	fxtest.New(t, fx.Populate(&cfg1), stdenvcfg.Provide[Conf1]())
	assert.Equal(t, "bar", cfg1.Foo)

	assert.Equal(t, "dar", string(cfg1.Bar))
	assert.Len(t, cfg1.Bars, 2)
	assert.Equal(t, "1", string(cfg1.Bars[0]))
	assert.Equal(t, "2", string(cfg1.Bars[1]))
}

func TestConfigProvidingPrefix(t *testing.T) {
	t.Setenv("FIX_FOO", "bar")

	var cfg1 Conf1
	fxtest.New(t, fx.Populate(&cfg1), stdenvcfg.Provide[Conf1]("FIX_"))
	assert.Equal(t, "bar", cfg1.Foo)
}
