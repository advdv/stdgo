package crpcauthtesting_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/advdv/stdgo/fx/stdcrpcauthfx"
	"github.com/advdv/stdgo/fx/stdcrpcauthfx/crpcauthtesting"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	internalv1 "github.com/advdv/stdgo/fx/stdcrpcauthfx/internal/v1"
)

func setup(tb testing.TB) (*stdcrpcauthfx.AccessControl, *crpcauthtesting.TokenSigner) {
	tb.Helper()

	jwksURL, signer := crpcauthtesting.NewJWKSServer(tb)

	var deps struct {
		fx.In
		AccessControl *stdcrpcauthfx.AccessControl
	}

	app := fxtest.New(tb,
		stdenvcfg.ProvideExplicitEnvironment(map[string]string{
			"TOKEN_ISSUER":   jwksURL,
			"TOKEN_AUDIENCE": crpcauthtesting.TestAudience,
		}),
		stdzapfx.Fx(),
		stdzapfx.TestProvide(tb),
		fx.Supply(fx.Annotate(crpcauthtesting.Clock(), fx.As(new(jwt.Clock)))),
		stdcrpcauthfx.Provide(),
		stdcrpcauthfx.ProtoExtensionScope(internalv1.E_RequiredPermission),
		fx.Populate(&deps),
	)
	app.RequireStart()
	tb.Cleanup(app.RequireStop)

	return deps.AccessControl, signer
}

func TestSignedTokenAuthenticated(t *testing.T) {
	t.Parallel()

	ac, signer := setup(t)
	token := signer.Sign(t, "test-user", []string{"system:read"})

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := stdcrpcauthfx.ClaimsFromContext(r.Context())
		fmt.Fprintf(w, "sub=%s scopes=%v", claims.Subject, claims.Scopes)
	})

	handler := ac.Wrap(inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost,
		"/fx.stdcrpcauthfx.internal.v1.SystemService/WhoAmI", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "sub=test-user")
	require.Contains(t, rec.Body.String(), "system:read")
}

func TestSignedTokenInsufficientScope(t *testing.T) {
	t.Parallel()

	ac, signer := setup(t)
	token := signer.Sign(t, "test-user", []string{"other:write"})

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := ac.Wrap(inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost,
		"/fx.stdcrpcauthfx.internal.v1.SystemService/WhoAmI", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestMissingToken(t *testing.T) {
	t.Parallel()

	ac, _ := setup(t)

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
