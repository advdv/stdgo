package stdhttpserverfx_test

import (
	"fmt"
	"net/http"
	"net/netip"
	"testing"

	"github.com/advdv/stdgo/fx/stdhttpserverfx"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

var serve = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

func TestServeNoName(t *testing.T) {
	var addr netip.AddrPort
	ctx, app := t.Context(), fx.New(

		fx.Supply(fx.Annotate(serve, fx.As(new(http.Handler)))),
		fx.Populate(&addr),
		stdenvcfg.ProvideOSEnvironment(),

		stdzapfx.Fx(),
		stdzapfx.TestProvide(t),
		stdhttpserverfx.Provide(),
	)

	require.NoError(t, app.Start(ctx))
	assertStatus(t, addr)
	require.NoError(t, app.Stop(ctx))

	_, err := http.Get(fmt.Sprintf("http://%s", addr.String())) //nolint:noctx,bodyclose
	require.ErrorContains(t, err, "connection refused")
}

func TestServeNamed(t *testing.T) {
	var addr netip.AddrPort
	ctx, app := t.Context(), fx.New(
		fx.Supply(fx.Annotate(serve, fx.ResultTags(`name:"web"`), fx.As(new(http.Handler)))),
		fx.Populate(fx.Annotate(&addr, fx.ParamTags(`name:"web"`))),
		stdenvcfg.ProvideOSEnvironment(),

		stdzapfx.Fx(),
		stdzapfx.TestProvide(t),
		stdhttpserverfx.Provide("web"),
	)

	require.NoError(t, app.Start(ctx))
	assertStatus(t, addr)
	require.NoError(t, app.Stop(ctx))
}

func TestTwoNamedServes(t *testing.T) {
	t.Setenv("STDHTTPSERVER_A_BIND_ADDR_PORT", "0.0.0.0:8181")
	t.Setenv("STDHTTPSERVER_B_BIND_ADDR_PORT", "0.0.0.0:8383")

	var deps struct {
		fx.In
		AddrA netip.AddrPort `name:"a"`
		AddrB netip.AddrPort `name:"b"`
	}

	app := fxtest.New(t,
		fx.Supply(fx.Annotate(serve, fx.ResultTags(`name:"a"`), fx.As(new(http.Handler)))),
		fx.Supply(fx.Annotate(serve, fx.ResultTags(`name:"b"`), fx.As(new(http.Handler)))),
		stdenvcfg.ProvideOSEnvironment(),

		stdhttpserverfx.Provide("a"),
		stdhttpserverfx.Provide("b"),
		stdzapfx.Fx(),
		stdzapfx.TestProvide(t),
		fx.Populate(&deps))

	app.RequireStart()
	assertStatus(t, deps.AddrA)
	assertStatus(t, deps.AddrB)
	t.Cleanup(app.RequireStop)
}

func assertStatus(tb testing.TB, addr netip.AddrPort) {
	resp, err := http.Get(fmt.Sprintf("http://%s", addr.String())) //nolint:noctx
	require.NoError(tb, err)
	defer resp.Body.Close()
	require.Equal(tb, http.StatusOK, resp.StatusCode)
}
