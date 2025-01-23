package stdawssecretsfx_test

import (
	"testing"

	"github.com/advdv/stdgo/fx/stdawsfx"
	"github.com/advdv/stdgo/fx/stdawssecretsfx"
	"github.com/aws/aws-secretsmanager-caching-go/v2/secretcache"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func TestCache(t *testing.T) {
	var cache *secretcache.Cache
	app := fxtest.New(t, stdawssecretsfx.Provide(), stdawsfx.Provide(), fx.Populate(&cache))
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	require.NotNil(t, cache)
}
