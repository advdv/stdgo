// Package stdcrpcaccesstest provides testing utilities for testing with access control.
package stdcrpcaccesstest

import (
	_ "embed"
	"fmt"
	"net/http"
	"net/http/httptest"

	"connectrpc.com/connect"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/lestrrat-go/jwx/v2/jwt/openid"
)

//go:embed test_jwks.json
var jwksData []byte

// TestKeyServer starts a server for testing that serves the key set.
func TestKeyServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(jwksData)
	}))
}

// SignToken signs a valid JWT against a well-known private key for testing.
func SignToken(tok openid.Token) (string, error) {
	jwks, err := jwk.Parse(jwksData)
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

// WithSignedToken is a http client middleware that always adds a valid (self signed) token for testing.
func WithSignedToken(base connect.HTTPClient, createToken func(r *http.Request) openid.Token) connect.HTTPClient {
	return withTestToken(func(r *http.Request) (*http.Response, error) {
		token, err := SignToken(createToken(r))
		if err != nil {
			return nil, fmt.Errorf("failed to sign test token: %w", err)
		}

		r.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

		return base.Do(r)
	})
}
