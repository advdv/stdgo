// Package insecureaccesstools provides insecure access token tools for testing.
package insecureaccesstools

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

const (
	// TestAccessTokenKID is the key ID used for signing test access tokens.
	TestAccessTokenKID = "test-access-token-key"
	// TestAccessTokenIssuer is the issuer claim used in test access tokens.
	TestAccessTokenIssuer = "https://id-dev.sterndesk.com/" //nolint:gosec // test constant, not a credential
	// TestAccessTokenAudience is the audience claim used in test access tokens.
	TestAccessTokenAudience = "basewarp-recode-api" //nolint:gosec // test constant, not a credential
	// TestAccessTokenSubject is the subject claim used in test access tokens.
	TestAccessTokenSubject = "google-oauth2|114814749289287160219" //nolint:gosec // test constant, not a credential
)

// NewJWKSServer starts an httptest.Server that serves a JWKS endpoint. It also returns a signed
// test access token that can be verified against the served JWKS. The server is automatically
// closed when the test completes.
func NewJWKSServer(tb testing.TB) (serverURL string, accessToken string) {
	tb.Helper()

	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		tb.Fatalf("generate RSA key: %v", err)
	}

	jwkKey, err := jwk.Import(privKey)
	if err != nil {
		tb.Fatalf("import jwk key: %v", err)
	}

	err = jwkKey.Set(jwk.KeyIDKey, TestAccessTokenKID)
	if err != nil {
		tb.Fatalf("set kid: %v", err)
	}

	err = jwkKey.Set(jwk.AlgorithmKey, jwa.RS256())
	if err != nil {
		tb.Fatalf("set alg: %v", err)
	}
	if err := jwkKey.Set(jwk.KeyUsageKey, "sig"); err != nil {
		tb.Fatalf("set use: %v", err)
	}

	pubKey, err := jwkKey.PublicKey()
	if err != nil {
		tb.Fatalf("get public key: %v", err)
	}

	pubSet := jwk.NewSet()
	if err := pubSet.AddKey(pubKey); err != nil {
		tb.Fatalf("add key to set: %v", err)
	}

	pubJSON, err := json.Marshal(pubSet)
	if err != nil {
		tb.Fatalf("marshal jwks: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(pubJSON) //nolint:errcheck
	}))
	tb.Cleanup(srv.Close)

	// Build and sign a test access token.
	now := time.Unix(1760813392, 0)

	tok, err := jwt.NewBuilder().
		Issuer(TestAccessTokenIssuer).
		Subject(TestAccessTokenSubject).
		Audience([]string{TestAccessTokenAudience, "https://basewarp-dev.eu.auth0.com/userinfo"}).
		IssuedAt(now).
		Expiration(now.Add(24*time.Hour)).
		Claim("scope", "openid").
		Claim("jti", "test-jti-value").
		Claim("client_id", "j9kQOGUCuZnwZiT9LMSz7oTI4JlMu9OU").
		Build()
	if err != nil {
		tb.Fatalf("build jwt: %v", err)
	}

	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256(), jwkKey))
	if err != nil {
		tb.Fatalf("sign jwt: %v", err)
	}

	return srv.URL, string(signed)
}
