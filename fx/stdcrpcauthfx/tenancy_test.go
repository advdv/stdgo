package stdcrpcauthfx_test

import (
	"testing"

	"connectrpc.com/authn"
	"github.com/advdv/stdgo/fx/stdcrpcauthfx"
	"github.com/advdv/stdgo/fx/stdcrpcenttenancyfx"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func TestProvideTenantIDResolverBindsClaimsTenantID(t *testing.T) {
	t.Parallel()

	var resolver stdcrpcenttenancyfx.TenantIDResolver

	app := fxtest.New(t,
		stdcrpcauthfx.ProvideTenantIDResolver(),
		fx.Populate(&resolver),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	require.NotNil(t, resolver)

	ctx := authn.SetInfo(t.Context(), stdcrpcauthfx.Claims{TenantID: "org_ABC123"})
	require.Equal(t, "org_ABC123", resolver.TenantIDFromContext(ctx))

	require.Empty(t, resolver.TenantIDFromContext(t.Context()),
		"resolver returns empty when no claims are stamped on ctx")
}
