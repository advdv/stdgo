package stdawssecretsfx_test

import (
	"testing"

	"github.com/advdv/stdgo/fx/stdawssecretsfx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/aws/aws-secretsmanager-caching-go/v2/secretcache"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func TestDecorate(t *testing.T) {
	cache, err := secretcache.New(func(c *secretcache.Cache) { c.Client = client1{} })
	require.NoError(t, err)

	var deps struct {
		fx.In
		Env stdenvcfg.Environment
	}

	app := fxtest.New(t,
		stdenvcfg.ProvideExplicitEnvironment(map[string]string{
			"FOO_BAR": "$$aws-secret-manager-resolve$$some:string:secret",
		}),
		stdawssecretsfx.DecorateEnvironment(),
		fx.Populate(&deps),
		fx.Supply(cache))
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	require.Equal(t, "sosecret", deps.Env["FOO_BAR"])
}
