package stdcrpcauthfx

import (
	"context"
	"net/http"

	"connectrpc.com/authn"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

type testClaimsKey struct{}

// WithTestClaims attaches Claims to the context for use with TestProvide.
// Each request can carry its own claims via its context.
func WithTestClaims(ctx context.Context, c Claims) context.Context {
	return context.WithValue(ctx, testClaimsKey{}, c)
}

// TestProvide provides the package's components as an fx module with a configuration for testing.
// It replaces Provide() in test fx.App setups, skipping JWT/JWKS validation entirely.
// Claims are read from the request context via WithTestClaims.
// The real Wrap() code path (scope resolution, permission checking) still runs.
func TestProvide() fx.Option {
	return fx.Module("stdcrpcauth-test",
		fx.Provide(func(logs *zap.Logger, sr ScopeResolver) *AccessControl {
			ac := &AccessControl{
				logs:          logs,
				scopeResolver: sr,
			}
			ac.middleware = authn.NewMiddleware(
				func(_ context.Context, req *http.Request) (any, error) {
					claims, ok := req.Context().Value(testClaimsKey{}).(Claims)
					if !ok {
						return nil, authn.Errorf("no test claims in context (use stdcrpcauthfx.WithTestClaims)")
					}

					return claims, nil
				},
			)

			return ac
		}),
	)
}
