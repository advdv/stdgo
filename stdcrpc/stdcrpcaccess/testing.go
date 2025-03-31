package stdcrpcaccess

import (
	_ "embed"
	"fmt"
	"net/http"
	"net/http/httptest"

	"connectrpc.com/connect"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"go.uber.org/fx"
)

//go:embed fixed_jwks_1.json
var fixedJwks1Data []byte

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
		fx.Provide(NewTestAuthBackend),

		// decorate by replacing with our test provider. This will make the rpc code call
		// our local test server instead of the real endpoint.
		fx.Decorate(func(_ AuthBackend, tap *TestAuthBackend) AuthBackend {
			return tap
		}),
	)
}

// NewTestAuthBackend starts a server for testing that serves the key set.
func NewTestAuthBackend() *TestAuthBackend {
	return &TestAuthBackend{httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(fixedJwks1Data)
	}))}
}

// SignTestToken signs a valid JWT against a well-known private key for testing.
func SignTestToken(tok jwt.Token) (string, error) {
	jwks, err := jwk.Parse(fixedJwks1Data)
	if err != nil {
		return "", fmt.Errorf("failed to parse jwk: %w", err)
	}

	sk, ok := jwks.LookupKeyID("key1")
	if !ok {
		return "", fmt.Errorf("no key with id jwk")
	}

	b, err := jwt.Sign(tok, jwt.WithKey(sk.Algorithm(), sk))
	if err != nil {
		return "", fmt.Errorf("failed to sign: %w", err)
	}

	return string(b), nil
}

type withTestToken func(*http.Request) (*http.Response, error)

func (f withTestToken) Do(r *http.Request) (*http.Response, error) { return f(r) }

// WithSignedTestToken is a http client middleware that always adds a valid (self signed) token for testing.
func WithSignedTestToken(base connect.HTTPClient, createToken func(r *http.Request) jwt.Token) connect.HTTPClient {
	return withTestToken(func(r *http.Request) (*http.Response, error) {
		token, err := SignTestToken(createToken(r))
		if err != nil {
			return nil, fmt.Errorf("failed to sign test token: %w", err)
		}

		r.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

		return base.Do(r)
	})
}
