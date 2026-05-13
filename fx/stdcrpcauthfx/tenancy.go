package stdcrpcauthfx

import (
	"context"

	"github.com/advdv/stdgo/fx/stdcrpcenttenancyfx"
	"go.uber.org/fx"
)

// ProvideTenantIDResolver returns an fx.Option that wires a
// [stdcrpcenttenancyfx.TenantIDResolver] backed by the JWT TenantID
// claim stamped on ctx by this package's authn middleware (see
// [Claims.TenantID] / [ClaimsFromContext]).
//
// Bundled here as a one-line convenience so composition roots that
// combine stdcrpcauthfx with stdcrpcenttenancyfx do not need to write
// the boilerplate closure that adapts [ClaimsFromContext] to the
// [stdcrpcenttenancyfx.TenantIDResolver] interface themselves.
//
// Usage:
//
//	fx.Options(
//	    stdcrpcauthfx.Provide(),
//	    stdcrpcenttenancyfx.Provide(),
//	    stdcrpcauthfx.ProvideTenantIDResolver(),
//	)
func ProvideTenantIDResolver() fx.Option {
	return fx.Provide(func() stdcrpcenttenancyfx.TenantIDResolver {
		return stdcrpcenttenancyfx.TenantIDResolverFunc(func(ctx context.Context) string {
			return ClaimsFromContext(ctx).TenantID
		})
	})
}
