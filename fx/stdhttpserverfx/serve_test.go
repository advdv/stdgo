package stdhttpserverfx_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"github.com/advdv/stdgo/fx/stdhttpserverfx"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

var serve = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

func TestServeNoName(t *testing.T) {
	t.Setenv("STDHTTPSERVER_READ_TIMEOUT", "60s")
	t.Setenv("STDHTTPSERVER_READ_HEADER_TIMEOUT", "2s")

	var (
		addr netip.AddrPort
		srv  *http.Server
	)

	ctx, app := t.Context(), fx.New(
		fx.Supply(fx.Annotate(serve, fx.As(new(http.Handler)))),
		fx.Populate(&addr, &srv),
		stdenvcfg.ProvideOSEnvironment(),

		stdzapfx.Fx(),
		stdzapfx.TestProvide(t),
		stdhttpserverfx.Provide(),
	)

	require.Equal(t, 60*time.Second, srv.ReadTimeout)
	require.Equal(t, 2*time.Second, srv.ReadHeaderTimeout)

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

func TestServeFailureShutsDown(t *testing.T) {
	t.Setenv("STDHTTPSERVER_BIND_ADDR_PORT", freeAddr(t))

	var srv *http.Server

	ctx, app := t.Context(), fx.New(
		fx.Supply(fx.Annotate(serve, fx.As(new(http.Handler)))),
		fx.Populate(&srv),
		stdenvcfg.ProvideOSEnvironment(),

		stdzapfx.Fx(),
		stdzapfx.TestProvide(t),
		stdhttpserverfx.Provide(),
	)

	// Serve hands its listener to BaseContext right before accepting, closing it makes Serve fail.
	srv.BaseContext = func(l net.Listener) context.Context {
		l.Close()

		return ctx
	}

	require.NoError(t, app.Start(ctx))

	select {
	case sig := <-app.Wait():
		require.Equal(t, 1, sig.ExitCode)
	case <-time.After(5 * time.Second):
		t.Fatal("expected a shutdown request after Serve failed")
	}

	require.NoError(t, app.Stop(ctx))
}

func TestPortNotBoundWhenAppFailsToInitialize(t *testing.T) {
	addr := freeAddr(t)
	t.Setenv("STDHTTPSERVER_BIND_ADDR_PORT", addr)

	app := fx.New(
		fx.Supply(fx.Annotate(serve, fx.As(new(http.Handler)))),
		stdenvcfg.ProvideOSEnvironment(),

		stdzapfx.Fx(),
		stdzapfx.TestProvide(t),
		stdhttpserverfx.Provide(),
		fx.Invoke(func(*http.Server) error { return errors.New("init failed") }),
	)
	require.ErrorContains(t, app.Err(), "init failed")

	lnr, err := net.Listen("tcp", addr)
	require.NoError(t, err)
	require.NoError(t, lnr.Close())
}

func freeAddr(tb testing.TB) string {
	tb.Helper()

	lnr, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(tb, err)
	require.NoError(tb, lnr.Close())

	return lnr.Addr().String()
}

func assertStatus(tb testing.TB, addr netip.AddrPort) {
	resp, err := http.Get(fmt.Sprintf("http://%s", addr.String())) //nolint:noctx
	require.NoError(tb, err)

	defer resp.Body.Close()

	require.Equal(tb, http.StatusOK, resp.StatusCode)
}
