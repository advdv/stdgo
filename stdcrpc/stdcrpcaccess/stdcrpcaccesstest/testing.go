// Package stdcrpcaccesstest provides testing utilities for testing with access control.
package stdcrpcaccesstest

import (
	_ "embed"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/advdv/stdgo/stdcrpc/stdcrpcaccess"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/lestrrat-go/jwx/v2/jwt/openid"
	"github.com/stretchr/testify/require"
)

//go:embed test_jwks.json
var jwksData []byte

// TestKeyServer starts a server for testing that serves the key set.
func TestKeyServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(jwksData)
	}))
}

// SignTestJWT signs a valid JWT against a well-known private key for testing.
func SignTestJWT(tb testing.TB, permissions []string) string {
	jwks, err := jwk.Parse(jwksData)
	require.NoError(tb, err)

	sk, ok := jwks.LookupKeyID("key1")
	require.True(tb, ok)

	tok := openid.New()
	require.NoError(tb, tok.Set("permissions", permissions))

	b, err := jwt.Sign(tok, jwt.WithKey(sk.Algorithm(), sk))
	require.NoError(tb, err)

	return string(b)
}

type withTestToken func(*http.Request) (*http.Response, error)

func (f withTestToken) Do(r *http.Request) (*http.Response, error) { return f(r) }

// WithTestToken is a http client middleware that always adds a valid (self signed) token for testing.
func WithTestToken(tb testing.TB, base connect.HTTPClient) connect.HTTPClient {
	return withTestToken(func(r *http.Request) (*http.Response, error) {
		permissions := stdcrpcaccess.PermissionsFromContext(r.Context())
		r.Header.Set("Authorization", fmt.Sprintf("Bearer %s", SignTestJWT(tb, permissions)))

		return base.Do(r)
	})
}
