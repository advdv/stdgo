package stdauthnfx_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/advdv/bhttp"
	"github.com/advdv/stdgo/fx/stdauthnfx"
	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"
)

func TestAuthentication(t *testing.T) {
	t.Parallel()
	authn := setup(t, stdauthnfx.NewAuthenticated("microsoft|cc77a22a-5d85-4fa3-b672-18e2d2221474", "ada@example.com"))
	require.NotNil(t, authn)

	t.Run("login", func(t *testing.T) {
		t.Parallel()

		path, hdlr := authn.Login()
		require.NotNil(t, hdlr)
		require.Equal(t, "/auth/{provider}/login", path)
		assertUnsupportedProvider(t, hdlr)

		cookies, redirect, err := call(t, hdlr, "google", "/?redirect_to=/")
		require.NoError(t, err)

		require.Len(t, cookies, 1)
		require.Equal(t, "AUTHSTATE", cookies[0].Name)

		t.Run("callback", func(t *testing.T) {
			t.Parallel()

			path, hdlr := authn.Callback()
			require.NotNil(t, hdlr)
			require.Equal(t, "/auth/{provider}/callback", path)

			cookies, redirect, err := call(t, hdlr, "google", "/?code=foo&state="+redirect.Query().Get("state"), cookies...)
			require.NoError(t, err)

			require.Equal(t, "/", redirect.String())

			require.Len(t, cookies, 2)
			require.Equal(t, "AUTHSTATE", cookies[0].Name)
			require.Equal(t, -1, cookies[0].MaxAge)

			require.Equal(t, "AUTHSESS", cookies[1].Name)
			require.Equal(t, 31556926, cookies[1].MaxAge)

			t.Run("middleware", func(t *testing.T) {
				rec, req := httptest.NewRecorder(), httptest.NewRequestWithContext(ctx(t), http.MethodGet, "/", nil)
				req.AddCookie(cookies[1])
				bresp := bhttp.NewResponseWriter(rec, -1)

				var idn stdauthnfx.Identity
				err := authn.SessionMiddleware()(bhttp.BareHandlerFunc(func(w bhttp.ResponseWriter, r *http.Request) error {
					idn = stdauthnfx.IdentityFromContext(r.Context())
					return nil
				})).ServeBareBHTTP(bresp, req)
				require.NoError(t, err)

				require.False(t, stdauthnfx.IsAnonymous(idn))
				authIdn, ok := idn.(stdauthnfx.Authenticated)
				require.True(t, ok)
				require.Equal(t, "ada@example.com", authIdn.Email())
			})
		})
	})

	t.Run("callback-without-cookie", func(t *testing.T) {
		t.Parallel()

		path, hdlr := authn.Callback()
		require.NotNil(t, hdlr)
		require.Equal(t, "/auth/{provider}/callback", path)
		assertUnsupportedProvider(t, hdlr)

		_, _, err := call(t, hdlr, "google", "/")
		require.ErrorContains(t, err, "code parameter not provided")
	})

	t.Run("anonymous", func(t *testing.T) {
		t.Parallel()

		rec, req := httptest.NewRecorder(), httptest.NewRequestWithContext(ctx(t), http.MethodGet, "/", nil)
		bresp := bhttp.NewResponseWriter(rec, -1)

		var idn stdauthnfx.Identity
		err := authn.SessionMiddleware()(bhttp.BareHandlerFunc(func(w bhttp.ResponseWriter, r *http.Request) error {
			idn = stdauthnfx.IdentityFromContext(r.Context())
			return nil
		})).ServeBareBHTTP(bresp, req)
		require.NoError(t, err)

		require.True(t, stdauthnfx.IsAnonymous(idn))
	})

	t.Run("logout", func(t *testing.T) {
		t.Parallel()
		path, hdlr := authn.Logout()
		require.NotNil(t, hdlr)
		require.Equal(t, "/auth/logout", path)

		rec, req := httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/?redirect_to=/", nil)
		bresp := bhttp.NewResponseWriter(rec, -1)
		hdlr.ServeBHTTP(ctx(t), bresp, req)

		require.Len(t, rec.Result().Cookies(), 1)
		require.Equal(t, "AUTHSESS", rec.Result().Cookies()[0].Name)
		require.Equal(t, -1, rec.Result().Cookies()[0].MaxAge)

		require.Equal(t, "/", rec.Header().Get("Location"))
	})

	t.Run("continue-session-fail", func(t *testing.T) {
		t.Parallel()

		rec, req := httptest.NewRecorder(), httptest.NewRequestWithContext(ctx(t), http.MethodGet, "/", nil)
		bresp := bhttp.NewResponseWriter(rec, -1)
		req.AddCookie(&http.Cookie{Name: "AUTHSESS"})

		var idn stdauthnfx.Identity
		err := authn.SessionMiddleware()(bhttp.BareHandlerFunc(func(w bhttp.ResponseWriter, r *http.Request) error {
			idn = stdauthnfx.IdentityFromContext(r.Context())
			return nil
		})).ServeBareBHTTP(bresp, req)
		require.Error(t, err, "Bad request: invalid session")
		require.Nil(t, idn) // since handler should never be reached in this case.
	})
}

func ctx(tb testing.TB) context.Context {
	return stdctx.WithLogger(tb.Context(), zap.NewNop())
}

func call(
	tb testing.TB, hdlr bhttp.Handler[context.Context], provider, target string, cookies ...*http.Cookie,
) ([]*http.Cookie, *url.URL, error) {
	rec, req := httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, target, nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	req.SetPathValue("provider", provider)

	bresp := bhttp.NewResponseWriter(rec, -1)
	err := hdlr.ServeBHTTP(ctx(tb), bresp, req)
	require.NoError(tb, bresp.FlushBuffer())
	if err != nil {
		return nil, nil, err
	}

	loc, err := url.Parse(rec.Result().Header.Get("Location"))
	require.NoError(tb, err)

	return rec.Result().Cookies(), loc, err
}

func assertUnsupportedProvider(tb testing.TB, hdlr bhttp.Handler[context.Context]) {
	_, _, err := call(tb, hdlr, "foo", "/")
	var herr *bhttp.Error
	require.ErrorAs(tb, err, &herr)
	require.Equal(tb, bhttp.CodeBadRequest, herr.Code())
}

func setup(tb testing.TB, idn stdauthnfx.Identity) *stdauthnfx.Authentication {
	var deps struct {
		fx.In
		*stdauthnfx.Authentication
	}

	app := fxtest.New(tb,
		fx.Supply(stdenvcfg.Environment{
			"STDAUTHN_BASE_CALLBACK_URL":    "http://localhost:8080/",
			"STDAUTHN_SESSION_KEY_PAIRS":    "cd111d0210ade2f4e9ae1c070bc83152fc64ea5a24dde4bd571f26a18c97f1f1,cd111d0210ade2f4e9ae1c070bc83152fc64ea5a24dde4bd571f26a18c97f1f1",
			"STDAUTHN_ENABLED_PROVIDERS":    "google",
			"STDAUTHN_GOOGLE_CLIENT_ID":     "g-client-id",
			"STDAUTHN_GOOGLE_CLIENT_SECRET": "g-client-secret",
			"STDAUTHN_GOOGLE_ISSUER":        "https://accounts.google.com",
		}),
		fx.Populate(&deps),
		stdauthnfx.Provide(),
		fx.Provide(func() stdauthnfx.Backend { return stdauthnfx.NewFixedIdentityBackend(idn) }),
	)

	app.RequireStart()
	tb.Cleanup(app.RequireStop)

	return deps.Authentication
}
