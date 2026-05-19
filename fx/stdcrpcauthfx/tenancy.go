package stdcrpcauthfx

import (
	"context"

	"connectrpc.com/authn"
	"github.com/advdv/stdgo/fx/stdcrpcenttenancyfx"
	"go.uber.org/fx"
)

// WithClaims stamps c onto ctx using the same surface as the
// authn middleware, so [ClaimsFromContext] (and consequently the
// [stdcrpcenttenancyfx.TenantIDResolver] wired by [ProvideTenantIDResolver])
// pick the value back up downstream.
//
// Intended for use at trusted seams that cannot run the authn middleware
// — most notably the Temporal context propagator
// ([github.com/advdv/stdgo/fx/stdcrpcauthfx/stdcrpcauthtemporalfx])
// re-stamping the activity ctx from the Temporal header. Production
// RPC code paths must NOT call this; claims belong on ctx via the
// authn middleware (or [WithTestClaims] in tests).
func WithClaims(ctx context.Context, c Claims) context.Context {
	return authn.SetInfo(ctx, c)
}

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
