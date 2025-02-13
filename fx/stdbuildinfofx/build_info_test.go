package stdbuildinfofx_test

import (
	"testing"

	"github.com/advdv/stdgo/fx/stdbuildinfofx"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func TestTestVersion(t *testing.T) {
	var binfo stdbuildinfofx.Info

	app := fxtest.New(t, stdbuildinfofx.TestProvide(), fx.Populate(&binfo))
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	require.Equal(t, `v0.0.0-test`, binfo.Version())
}

func TestVersion(t *testing.T) {
	var binfo stdbuildinfofx.Info

	app := fxtest.New(t, stdbuildinfofx.Provide("dev.dev.dev"), fx.Populate(&binfo))
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	require.Equal(t, `dev.dev.dev`, binfo.Version())
}
