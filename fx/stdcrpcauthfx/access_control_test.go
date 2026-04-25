package stdcrpcauthfx_test

import (
	"context"
	"fmt"
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
			"TOKEN_ISSUER":   "https://cmplback-nonprod.eu.auth0.com/",
			"TOKEN_AUDIENCE": "urn:sterndesk:cmplback:cmpltemporal:api",
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

	serverURL, signer := crpcauthtesting.NewJWKSServer(tb)

	var deps struct {
		fx.In

		AccessControl *stdcrpcauthfx.AccessControl
	}

	app := fxtest.New(tb,
		stdzapfx.Fx(),
		stdzapfx.TestProvide(tb),
		stdenvcfg.ProvideExplicitEnvironment(map[string]string{
			"TOKEN_ISSUER":   serverURL,
			"TOKEN_AUDIENCE": crpcauthtesting.TestAudience,
		}),
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
}
