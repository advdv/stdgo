package stdcrpcaccesstest

import (
	"net/http"
	"net/http/httptest"

	"github.com/advdv/stdgo/stdcrpc/stdcrpcaccess"
	"go.uber.org/fx"
)

// TestAuthBackend is an auth backend that is run locally and we control the signing process for.
type TestAuthBackend struct {
	https *httptest.Server
}

func (ap TestAuthBackend) JWKSEndpoint() string {
	return ap.https.URL
}

// WithTestAuthBackend injects dependencies for allowing tests to sign and validate access tokens.
func WithTestAuthBackend() fx.Option {
	return fx.Options(
		// provide a auth backend that returns jwks locally. so we have them under control.
		fx.Provide(func() *TestAuthBackend {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(jwksData)
			}))

			return &TestAuthBackend{srv}
		}),

		// decorate by replacing with our test provider. This will make the rpc code call
		// our local test server instead of the real endpoint.
		fx.Decorate(func(_ stdcrpcaccess.AuthBackend, tap *TestAuthBackend) stdcrpcaccess.AuthBackend {
			return tap
		}),
	)
}
