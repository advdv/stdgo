package stdcrpcauthfx_test

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/advdv/stdgo/fx/stdcrpcauthfx"
	"github.com/advdv/stdgo/fx/stdcrpcauthfx/crpcauthtesting"
	internalv1 "github.com/advdv/stdgo/fx/stdcrpcauthfx/internal/v1"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func setup(tb testing.TB) *stdcrpcauthfx.AccessControl {
	tb.Helper()

	var deps struct {
		fx.In

		AccessControl *stdcrpcauthfx.AccessControl
	}

	app := fxtest.New(tb,
		stdzapfx.Fx(),
		stdzapfx.TestProvide(tb),
		stdenvcfg.ProvideExplicitEnvironment(map[string]string{
			"STDCRPCAUTH_TOKEN_ISSUER":   "https://cmplback-nonprod.eu.auth0.com/",
			"STDCRPCAUTH_TOKEN_AUDIENCE": "urn:sterndesk:cmplback:cmpltemporal:api",
		}),
		fx.Supply(fx.Annotate(
			jwt.ClockFunc(func() time.Time { return time.Unix(1776691700, 0) }),
			fx.As(new(jwt.Clock)),
		)),
		stdcrpcauthfx.Provide(),
		stdcrpcauthfx.ProtoExtensionScope(internalv1.E_RequiredPermission),
		fx.Populate(&deps),
	)
	app.RequireStart()
	tb.Cleanup(app.RequireStop)

	return deps.AccessControl
}

func readTestToken(tb testing.TB) string {
	tb.Helper()

	tokenBytes, err := os.ReadFile(filepath.Join("testdata", "m2m_token.jwt"))
	require.NoError(tb, err)

	return string(tokenBytes)
}

func TestWrapAuthenticated(t *testing.T) {
	t.Parallel()

	ac := setup(t)
	token := readTestToken(t)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := stdcrpcauthfx.ClaimsFromContext(r.Context())
		fmt.Fprintf(w, "sub=%s scopes=%v", claims.Subject, claims.Scopes)
	})

	handler := ac.Wrap(inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(
		context.Background(), http.MethodPost,
		"/fx.stdcrpcauthfx.internal.v1.SystemService/WhoAmI", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "sub=NyPg4cptuoPwgxRBtSf0Ln1Uaw8lwpLZ@clients")
	require.Contains(t, rec.Body.String(), "system:read")
}

func TestWrapMissingToken(t *testing.T) {
	t.Parallel()

	ac := setup(t)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := ac.Wrap(inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(
		context.Background(), http.MethodPost,
		"/fx.stdcrpcauthfx.internal.v1.SystemService/WhoAmI", nil)

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestWrapInvalidToken(t *testing.T) {
	t.Parallel()

	ac := setup(t)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := ac.Wrap(inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(
		context.Background(), http.MethodPost,
		"/fx.stdcrpcauthfx.internal.v1.SystemService/WhoAmI", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestWrapNonProcedurePassesThrough(t *testing.T) {
	t.Parallel()

	ac := setup(t)
	token := readTestToken(t)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := ac.Wrap(inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

func setupLocal(tb testing.TB) (*stdcrpcauthfx.AccessControl, *crpcauthtesting.TokenSigner) {
	tb.Helper()

	return setupLocalWithEnv(tb, nil)
}

func setupLocalWithEnv(
	tb testing.TB, extraEnv map[string]string,
) (*stdcrpcauthfx.AccessControl, *crpcauthtesting.TokenSigner) {
	tb.Helper()

	// keep the signer's tenant-claim path in lockstep with whatever the
	// fx-supplied env tells the production middleware to read.
	serverURL, signer := crpcauthtesting.NewJWKSServer(tb, extraEnv["STDCRPCAUTH_TENANT_CLAIM"])

	env := map[string]string{
		"STDCRPCAUTH_TOKEN_ISSUER":   serverURL,
		"STDCRPCAUTH_TOKEN_AUDIENCE": crpcauthtesting.TestAudience,
	}
	maps.Copy(env, extraEnv)

	var deps struct {
		fx.In

		AccessControl *stdcrpcauthfx.AccessControl
	}

	app := fxtest.New(tb,
		stdzapfx.Fx(),
		stdzapfx.TestProvide(tb),
		stdenvcfg.ProvideExplicitEnvironment(env),
		fx.Supply(fx.Annotate(
			crpcauthtesting.Clock(),
			fx.As(new(jwt.Clock)),
		)),
		stdcrpcauthfx.Provide(),
		stdcrpcauthfx.ProtoExtensionScope(internalv1.E_RequiredPermission),
		fx.Populate(&deps),
	)
	app.RequireStart()
	tb.Cleanup(app.RequireStop)

	return deps.AccessControl, signer
}

func TestWrapPermissionsClaim(t *testing.T) {
	t.Parallel()

	ac, signer := setupLocal(t)
	token := signer.SignWithPermissions(t, "auth0|user123", []string{"system:read"})

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := stdcrpcauthfx.ClaimsFromContext(r.Context())
		fmt.Fprintf(w, "sub=%s scopes=%v", claims.Subject, claims.Scopes)
	})

	handler := ac.Wrap(inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(
		context.Background(), http.MethodPost,
		"/fx.stdcrpcauthfx.internal.v1.SystemService/WhoAmI", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "sub=auth0|user123")
	require.Contains(t, rec.Body.String(), "system:read")
}

func TestWrapPermissionsClaimInsufficientScope(t *testing.T) {
	t.Parallel()

	ac, signer := setupLocal(t)
	token := signer.SignWithPermissions(t, "auth0|user123", []string{"other:write"})

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := ac.Wrap(inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(
		context.Background(), http.MethodPost,
		"/fx.stdcrpcauthfx.internal.v1.SystemService/WhoAmI", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestWrapM2MTokenNoDuplicateScopes(t *testing.T) {
	t.Parallel()

	ac, signer := setupLocal(t)
	// Simulates an Auth0 m2m token where the same scope appears in both
	// the "scope" string claim and the "permissions" array claim.
	token := signer.SignWithScopeAndPermissions(t,
		"227e0I19bqG0IwK6QAWHWb0xOvLJnMmV@clients",
		[]string{"system:read"},
		[]string{"system:read"})

	var captured stdcrpcauthfx.Claims
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = stdcrpcauthfx.ClaimsFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := ac.Wrap(inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(
		context.Background(), http.MethodPost,
		"/fx.stdcrpcauthfx.internal.v1.SystemService/WhoAmI", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, []string{"system:read"}, captured.Scopes,
		"scope appearing in both 'scope' and 'permissions' must not be duplicated")
}

func TestClaimsFromContextEmpty(t *testing.T) {
	t.Parallel()

	claims := stdcrpcauthfx.ClaimsFromContext(t.Context())

	require.Empty(t, claims.Subject)
	require.Empty(t, claims.Scopes)
	require.Empty(t, claims.TenantID)
}

// captureClaims runs an authenticated request through ac.Wrap and returns the
// claims observed by the inner handler.
func captureClaims(
	t *testing.T, ac *stdcrpcauthfx.AccessControl, token string,
) (int, stdcrpcauthfx.Claims) {
	t.Helper()

	var captured stdcrpcauthfx.Claims
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = stdcrpcauthfx.ClaimsFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost,
		"/fx.stdcrpcauthfx.internal.v1.SystemService/WhoAmI", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	ac.Wrap(inner).ServeHTTP(rec, req)

	return rec.Code, captured
}

const testTenantClaim = "https://example.com/org_id"

func TestWrapTenantIDExtracted(t *testing.T) {
	t.Parallel()

	ac, signer := setupLocalWithEnv(t, map[string]string{
		"STDCRPCAUTH_TENANT_CLAIM": testTenantClaim,
	})
	token := signer.SignWithClaims(t, "auth0|user123", []string{"system:read"},
		map[string]any{testTenantClaim: "org_ABC123"})

	code, claims := captureClaims(t, ac, token)

	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "org_ABC123", claims.TenantID)
	require.Equal(t, "auth0|user123", claims.Subject)
	require.Equal(t, []string{"system:read"}, claims.Scopes)
}

func TestWrapTenantIDMissingClaimWhenConfigured(t *testing.T) {
	t.Parallel()

	// TENANT_CLAIM is configured but the token does not carry the claim. This
	// must not fail authentication; TenantID is simply empty and the consumer
	// decides whether absent tenancy is acceptable for this procedure.
	ac, signer := setupLocalWithEnv(t, map[string]string{
		"STDCRPCAUTH_TENANT_CLAIM": testTenantClaim,
	})
	token := signer.Sign(t, "auth0|user123", []string{"system:read"})

	code, claims := captureClaims(t, ac, token)

	require.Equal(t, http.StatusOK, code)
	require.Empty(t, claims.TenantID)
}

func TestWrapTenantIDNotConfiguredIgnoresClaim(t *testing.T) {
	t.Parallel()

	// TENANT_CLAIM is unset. Even if the token carries something at the path
	// some other deployment uses, this deployment must not pick it up.
	ac, signer := setupLocal(t)
	token := signer.SignWithClaims(t, "auth0|user123", []string{"system:read"},
		map[string]any{testTenantClaim: "org_should_be_ignored"})

	code, claims := captureClaims(t, ac, token)

	require.Equal(t, http.StatusOK, code)
	require.Empty(t, claims.TenantID,
		"TenantID must be empty when TENANT_CLAIM is not configured")
}

func TestWrapTenantIDNonStringClaimIsIgnored(t *testing.T) {
	t.Parallel()

	// A claim of the wrong shape (here, an array) must not panic and must
	// leave TenantID empty rather than silently coercing.
	ac, signer := setupLocalWithEnv(t, map[string]string{
		"STDCRPCAUTH_TENANT_CLAIM": testTenantClaim,
	})
	token := signer.SignWithClaims(t, "auth0|user123", []string{"system:read"},
		map[string]any{testTenantClaim: []string{"not", "a", "string"}})

	code, claims := captureClaims(t, ac, token)

	require.Equal(t, http.StatusOK, code)
	require.Empty(t, claims.TenantID)
}
