package stdenvcfg_test

import (
	"testing"

	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/stretchr/testify/assert"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

type Conf1 struct {
	Foo string `env:"FOO"`
}

func TestConfigProviding(t *testing.T) {
	t.Setenv("FOO", "bar")

	var cfg1 Conf1
	fxtest.New(t, fx.Populate(&cfg1), stdenvcfg.Provide[Conf1]())
	assert.Equal(t, "bar", cfg1.Foo)
}

func TestConfigProvidingPrefix(t *testing.T) {
	t.Setenv("FIX_FOO", "bar")

	var cfg1 Conf1
	fxtest.New(t, fx.Populate(&cfg1), stdenvcfg.Provide[Conf1]("FIX_"))
	assert.Equal(t, "bar", cfg1.Foo)
}
