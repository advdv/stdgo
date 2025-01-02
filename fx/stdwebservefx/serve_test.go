package stdwebservefx_test

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"testing"

	"github.com/advdv/stdgo/fx/stdwebservefx"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

func TestServe(t *testing.T) {
	serve := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	var addr netip.AddrPort
	ctx, app := context.Background(), fx.New(
		fx.Supply(fx.Annotate(serve, fx.As(new(http.Handler)))),
		fx.Provide(zap.NewExample),
		fx.Populate(&addr),

		stdwebservefx.Provide(),
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
