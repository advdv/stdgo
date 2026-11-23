package stdbifrosttestfx_test

import (
	"testing"

	"github.com/advdv/stdgo/fx/stdbifrostfx"
	"github.com/advdv/stdgo/fx/stdbifrostfx/stdbifrosttestfx"
	"github.com/advdv/stdgo/fx/stdhttpclientfx"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

// The test replays testdata/llm/TestReplay.yaml, without a gateway.
func TestReplay(t *testing.T) {
	t.Parallel()

	var client *stdbifrostfx.Client

	app := fxtest.New(t,
		stdenvcfg.ProvideExplicitEnvironment(map[string]string{"STDBIFROST_MODEL": "provider/model"}),
		stdzapfx.Fx(),
		stdzapfx.TestProvide(t),
		stdhttpclientfx.Provide(),
		stdbifrosttestfx.Provide(t, stdbifrosttestfx.Gateway{}),
		fx.Populate(&client),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	tokens, err := client.FirstToken(t.Context(), "Is it?")
	require.NoError(t, err)
	require.Equal(t, map[string]float64{"yes": 1}, tokens)
}
