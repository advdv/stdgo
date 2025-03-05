package stdhttpserverfx_test

import (
	"fmt"
	"net/http"
	"net/netip"
	"testing"

	"github.com/advdv/stdgo/fx/stdhttpserverfx"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
)

func TestServe(t *testing.T) {
	serve := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	var addr netip.AddrPort
	ctx, app := t.Context(), fx.New(
		fx.Supply(fx.Annotate(serve, fx.As(new(http.Handler)))),
		fx.Populate(&addr),

		stdzapfx.Fx(),
		stdzapfx.TestProvide(t),
		stdhttpserverfx.Provide(),
	)

	require.NoError(t, app.Start(ctx))

	resp, err := http.Get(fmt.Sprintf("http://%s", addr.String())) //nolint:noctx
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.NoError(t, app.Stop(ctx))

	_, err = http.Get(fmt.Sprintf("http://%s", addr.String())) //nolint:noctx,bodyclose
	require.ErrorContains(t, err, "connection refused")
}
