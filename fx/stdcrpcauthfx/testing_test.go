package stdcrpcauthfx_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/advdv/stdgo/fx/stdcrpcauthfx"
	internalv1 "github.com/advdv/stdgo/fx/stdcrpcauthfx/internal/v1"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func testSetup(tb testing.TB) *stdcrpcauthfx.AccessControl {
	tb.Helper()

	var deps struct {
		fx.In

		AccessControl *stdcrpcauthfx.AccessControl
	}

	app := fxtest.New(tb,
		stdenvcfg.ProvideExplicitEnvironment(nil),
		stdzapfx.Fx(),
		stdzapfx.TestProvide(tb),
		stdcrpcauthfx.TestProvide(),
		stdcrpcauthfx.ProtoExtensionScope(internalv1.E_RequiredPermission),
		fx.Populate(&deps),
	)
	app.RequireStart()
	tb.Cleanup(app.RequireStop)

	return deps.AccessControl
}

func TestTestProvideAuthenticated(t *testing.T) {
	t.Parallel()

	ac := testSetup(t)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := stdcrpcauthfx.ClaimsFromContext(r.Context())
		fmt.Fprintf(w, "sub=%s scopes=%v", claims.Subject, claims.Scopes)
	})

	handler := ac.Wrap(inner)

	ctx := stdcrpcauthfx.WithTestClaims(t.Context(), stdcrpcauthfx.Claims{
		Subject: "test-user",
		Scopes:  []string{"system:read"},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(
		ctx, http.MethodPost,
		"/fx.stdcrpcauthfx.internal.v1.SystemService/WhoAmI", nil)

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "sub=test-user")
	require.Contains(t, rec.Body.String(), "system:read")
}

func TestTestProvideInsufficientScope(t *testing.T) {
	t.Parallel()

	ac := testSetup(t)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := ac.Wrap(inner)

	ctx := stdcrpcauthfx.WithTestClaims(t.Context(), stdcrpcauthfx.Claims{
		Subject: "test-user",
		Scopes:  []string{"other:write"},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(
		ctx, http.MethodPost,
		"/fx.stdcrpcauthfx.internal.v1.SystemService/WhoAmI", nil)

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestTestProvideMissingClaims(t *testing.T) {
	t.Parallel()

	ac := testSetup(t)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := ac.Wrap(inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost,
		"/fx.stdcrpcauthfx.internal.v1.SystemService/WhoAmI", nil)

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestTestProvideNonProcedurePassesThrough(t *testing.T) {
	t.Parallel()

	ac := testSetup(t)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := ac.Wrap(inner)

	ctx := stdcrpcauthfx.WithTestClaims(t.Context(), stdcrpcauthfx.Claims{
		Subject: "test-user",
		Scopes:  []string{"system:read"},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/healthz", nil)

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}
