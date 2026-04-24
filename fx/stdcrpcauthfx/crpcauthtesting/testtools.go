// Package crpcauthtesting provides test helpers for stdcrpcauthfx that use real JWT signing and
// validation. A local JWKS server is started so the real authentication code path runs in tests.
package crpcauthtesting

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

const (
	// TestAudience is the audience claim used in test JWTs.
	TestAudience = "urn:test:audience"

	// TestKeyID is the key ID used for signing test JWTs.
	TestKeyID = "test-key"
)

// TestClockTime is the fixed wall-clock time used for JWT validation in tests.
var TestClockTime = time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

// TokenSigner signs JWTs using a test RSA key pair.
type TokenSigner struct {
	key      jwk.Key
	issuer   string
	audience string
}

// Sign creates a signed JWT with the given subject and scopes (via the "scope" claim).
func (s *TokenSigner) Sign(tb testing.TB, subject string, scopes []string) string {
	tb.Helper()

	tok, err := jwt.NewBuilder().
		Issuer(s.issuer).
		Audience([]string{s.audience}).
		Subject(subject).
		IssuedAt(TestClockTime.Add(-time.Minute)).
		Expiration(TestClockTime.Add(24*time.Hour)).
		Claim("scope", strings.Join(scopes, " ")).
		Build()
	if err != nil {
		tb.Fatalf("testtools: build jwt: %v", err)
	}

	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256(), s.key))
	if err != nil {
		tb.Fatalf("testtools: sign jwt: %v", err)
	}

	return string(signed)
}

// SignWithPermissions creates a signed JWT with the given subject and permissions
// (via the "permissions" claim as a JSON array, matching the Auth0 SPA token format).
func (s *TokenSigner) SignWithPermissions(tb testing.TB, subject string, permissions []string) string {
	tb.Helper()

	tok, err := jwt.NewBuilder().
		Issuer(s.issuer).
		Audience([]string{s.audience}).
		Subject(subject).
		IssuedAt(TestClockTime.Add(-time.Minute)).
		Expiration(TestClockTime.Add(24*time.Hour)).
		Claim("scope", "openid profile email").
		Claim("permissions", permissions).
		Build()
	if err != nil {
		tb.Fatalf("testtools: build jwt: %v", err)
	}

	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256(), s.key))
	if err != nil {
		tb.Fatalf("testtools: sign jwt: %v", err)
	}

	return string(signed)
}

// Clock returns a jwt.Clock fixed to TestClockTime, suitable for fx.Supply.
func Clock() jwt.Clock {
	return jwt.ClockFunc(func() time.Time { return TestClockTime })
}

// NewJWKSServer starts a local JWKS httptest.Server and returns the server URL and a TokenSigner.
// The server is automatically closed when the test completes. The server URL can be used as
// TOKEN_ISSUER and the server serves the public key at /.well-known/jwks.json.
func NewJWKSServer(tb testing.TB) (serverURL string, signer *TokenSigner) {
	tb.Helper()

	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		tb.Fatalf("testtools: generate RSA key: %v", err)
	}

	jwkKey, err := jwk.Import(privKey)
	if err != nil {
		tb.Fatalf("testtools: import jwk key: %v", err)
	}

	if err := jwkKey.Set(jwk.KeyIDKey, TestKeyID); err != nil {
		tb.Fatalf("testtools: set kid: %v", err)
	}

	if err := jwkKey.Set(jwk.AlgorithmKey, jwa.RS256()); err != nil {
		tb.Fatalf("testtools: set alg: %v", err)
	}

	if err := jwkKey.Set(jwk.KeyUsageKey, "sig"); err != nil {
		tb.Fatalf("testtools: set use: %v", err)
	}

	pubKey, err := jwkKey.PublicKey()
	if err != nil {
		tb.Fatalf("testtools: get public key: %v", err)
	}

	pubSet := jwk.NewSet()
	if err := pubSet.AddKey(pubKey); err != nil {
		tb.Fatalf("testtools: add key to set: %v", err)
	}

	pubJSON, err := json.Marshal(pubSet)
	if err != nil {
		tb.Fatalf("testtools: marshal jwks: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/jwks.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(pubJSON) //nolint:errcheck
	})

	srv := httptest.NewServer(mux)
	tb.Cleanup(srv.Close)

	return srv.URL, &TokenSigner{
		key:      jwkKey,
		issuer:   srv.URL,
		audience: TestAudience,
	}
}
