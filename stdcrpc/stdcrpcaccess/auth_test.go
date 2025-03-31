package stdcrpcaccess_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "embed"

	"github.com/advdv/stdgo/stdcrpc/stdcrpcaccess"
	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdlo"
	"github.com/go-viper/mapstructure/v2"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

//go:embed fixed_jwks_2.json
var fixedJwks2Data []byte

func buildTestToken(tb testing.TB, primaryIdentity string, perms ...string) jwt.Token {
	tok, err := jwt.NewBuilder().
		Subject(primaryIdentity).
		Claim("permissions", perms).
		Issuer("auth-backend").
		Audience([]string{"access-test"}).Build()
	require.NoError(tb, err)
	return tok
}

func TestCheckAuth(t *testing.T) {
	tsrv := stdcrpcaccess.NewTestAuthBackend()

	token1 := buildTestToken(t, "foo|user-1", "/a/b", "/x/y")
	validToken1, err := stdcrpcaccess.SignTestToken(token1)
	require.NoError(t, err)

	for _, tt := range []struct {
		name       string
		path       string
		expCode    int
		expJSON    string
		setHdr     func(http.Header)
		assertLogs func(t *testing.T, obs *observer.ObservedLogs)
	}{
		{
			"no token", "/a", http.StatusUnauthorized,
			`{"code":"unauthenticated", "message":"no token"}`,
			func(h http.Header) {},
			func(t *testing.T, obs *observer.ObservedLogs) {
				t.Helper()
				require.Empty(t, obs.All())
			},
		},

		{
			"invalid token", "/a", http.StatusUnauthorized,
			`{"code":"unauthenticated", "message":"invalid token"}`,
			func(h http.Header) {
				h.Set("X-Amzn-Oidc-Accesstoken", "foo.bar.dar")
			},
			func(t *testing.T, obs *observer.ObservedLogs) {
				t.Helper()
				require.Len(t, obs.FilterMessage("client provided invalid token").All(), 1)
				require.Len(t, obs.FilterMessage("authenticating token").All(), 1)
			},
		},
		{
			"invalid procedure", "/a", http.StatusForbidden,
			`{"code":"permission_denied", "message":"unable to determine RPC procedure"}`,
			func(h http.Header) {
				h.Set("X-Amzn-Oidc-Accesstoken", validToken1)
			},
			func(t *testing.T, obs *observer.ObservedLogs) {
				t.Helper()
				require.Len(t, obs.FilterMessage("authenticating token").All(), 1)
			},
		},
		{
			"no permission", "/b/c", http.StatusForbidden,
			`{"code":"permission_denied", "message":"procedure not allowed"}`,
			func(h http.Header) {
				h.Set("X-Amzn-Oidc-Accesstoken", validToken1)
			},
			func(t *testing.T, obs *observer.ObservedLogs) {
				t.Helper()
				require.Len(t, obs.FilterMessage("authenticating token").All(), 1)
				require.Len(t, obs.FilterMessage("authorizing token").All(), 1)
				require.Len(t, obs.FilterMessage("access to procedure denied").All(), 1)
			},
		},

		{
			"ok", "/a/b", http.StatusOK,
			`{"permissions":["/a/b", "/x/y"],"primary_identity":"foo|user-1"}`,
			func(h http.Header) {
				h.Set("X-Amzn-Oidc-Accesstoken", validToken1)
			},
			func(t *testing.T, obs *observer.ObservedLogs) {
				t.Helper()
			},
		},

		{
			"ok-anonymous", "/p/public", http.StatusOK,
			`{"permissions":["/p/public"],"primary_identity":""}`,
			func(h http.Header) {
			},
			func(t *testing.T, obs *observer.ObservedLogs) {
				t.Helper()
				require.Len(t, obs.FilterMessage("authorizing anonymous access").All(), 1)
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			core, obs := observer.New(zapcore.DebugLevel)
			logs := zap.New(core)

			ac := stdcrpcaccess.New(
				authLogic{},
				tsrv,
				jwk.NewSet(),
				"access-test",
				"auth-backend",
				"self-sign",
				nil)

			rec, req := httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, tt.path, nil)
			tt.setHdr(req.Header)
			req = req.WithContext(stdctx.WithLogger(ctx, logs))

			ac.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				info := infoFromContext(r.Context())

				json.NewEncoder(w).Encode(map[string]any{
					"primary_identity": info.PrimaryIdentity,
					"permissions":      info.Permissions,
				})
			})).ServeHTTP(rec, req)

			require.Equal(t, tt.expCode, rec.Code)
			require.JSONEq(t, tt.expJSON, rec.Body.String())
			tt.assertLogs(t, obs)

			require.NoError(t, ac.Close(ctx))
		})
	}
}

func TestSigning(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		keys, err := jwk.Parse(fixedJwks2Data)
		require.NoError(t, err)

		zc, _ := observer.New(zap.DebugLevel)

		logs := zap.New(zc)
		ctx := stdctx.WithLogger(t.Context(), logs)

		tsrv := stdcrpcaccess.NewTestAuthBackend()
		ac := stdcrpcaccess.New(
			authLogic{},
			tsrv,
			keys,
			"access-test",
			"auth-backend",
			"self-sign",
			nil)

		bldr := jwt.NewBuilder().Claim("permissions", []string{"/a/b"})
		token, err := ac.SignAccessToken(bldr, "key2")
		require.NoError(t, err)

		rec, req := httptest.NewRecorder(), httptest.NewRequestWithContext(ctx, http.MethodGet, "/a/b", nil)
		req.Header.Set("Authorization", "Bearer "+string(token))

		ac.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Result().StatusCode)
	})

	t.Run("with-organization", func(t *testing.T) {
		keys, err := jwk.Parse(fixedJwks2Data)
		require.NoError(t, err)

		ctx := stdctx.WithLogger(t.Context(), zap.NewNop())

		tsrv := stdcrpcaccess.NewTestAuthBackend()
		ac := stdcrpcaccess.New(
			authLogic{},
			tsrv,
			keys,
			"access-test",
			"auth-backend",
			"self-sign",
			[]jwt.Validator{jwt.ClaimValueIs("organization", "org1")})

		bldr := jwt.NewBuilder().Claim("organization", "org1").Claim("permissions", []string{"/a/b"})
		token, err := ac.SignAccessToken(bldr, "key2")
		require.NoError(t, err)

		rec, req := httptest.NewRecorder(), httptest.NewRequestWithContext(ctx, http.MethodGet, "/a/b", nil)
		req.Header.Set("Authorization", "Bearer "+string(token))

		ac.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Result().StatusCode)
	})

	t.Run("invalid", func(t *testing.T) {
		keys, err := jwk.Parse(fixedJwks2Data)
		require.NoError(t, err)

		zc, obs := observer.New(zap.DebugLevel)

		logs := zap.New(zc)
		ctx := stdctx.WithLogger(t.Context(), logs)

		tsrv := stdcrpcaccess.NewTestAuthBackend()
		ac := stdcrpcaccess.New(
			authLogic{},
			tsrv,
			keys,
			"access-test",
			"auth-backend",
			"self-sign",
			nil)

		bldr := jwt.NewBuilder().Claim("permissions", []string{"/a/b"}).NotBefore(time.Now().Add(time.Hour))
		token, err := ac.SignAccessToken(bldr, "key2")
		require.NoError(t, err)

		rec, req := httptest.NewRecorder(), httptest.NewRequestWithContext(ctx, http.MethodGet, "/a/b", nil)
		req.Header.Set("Authorization", "Bearer "+string(token))

		ac.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(rec, req)

		require.Equal(t, http.StatusUnauthorized, rec.Result().StatusCode)
		require.Len(t, obs.FilterMessage("client provided invalid token").All(), 1)
	})
}

func TestWithHTTPClient(t *testing.T) {
	tsrv := stdcrpcaccess.NewTestAuthBackend()
	ac := stdcrpcaccess.New(
		authLogic{},
		tsrv,
		jwk.NewSet(),
		"access-test", "auth-backend", "self-sign", nil)

	zc, _ := observer.New(zap.DebugLevel)
	logs := zap.New(zc)

	inner := ac.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := infoFromContext(r.Context())
		json.NewEncoder(w).Encode(map[string]any{
			"primary_identity": info.PrimaryIdentity,
			"permissions":      info.Permissions,
		})
	}))

	outer := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner.ServeHTTP(w, r.WithContext(stdctx.WithLogger(r.Context(), logs)))
	}))

	srv := httptest.NewServer(outer)

	ctx := t.Context()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/a/b", nil)

	token1 := buildTestToken(t, "foo-1", "/a/b")
	cln := stdcrpcaccess.WithSignedTestToken(srv.Client(), func(r *http.Request) jwt.Token { return token1 })
	resp, err := cln.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })

	body := stdlo.Must1(io.ReadAll(resp.Body))
	require.JSONEq(t, `{"permissions":["/a/b"],"primary_identity":"foo-1"}`, string(body))
	require.Equal(t, 200, resp.StatusCode)
}

type authLogic struct{}

func (authLogic) ProcedurePermissions(info authInfo) []string {
	return stdlo.Map(info.Permissions, func(s string, _ int) string { return s })
}

func (authLogic) DecorateContext(ctx context.Context, info authInfo) context.Context {
	return context.WithValue(ctx, ctxKey("info"), info)
}

func (authLogic) InitFromAccessToken(ctx context.Context, tok jwt.Token) (authInfo, error) {
	info := authInfo{PrimaryIdentity: tok.Subject()}
	if err := mapstructure.Decode(tok.PrivateClaims(), &info); err != nil {
		return info, fmt.Errorf("decode private claims: %w", err)
	}

	return info, nil
}

func (authLogic) InitAsAnonymous(ctx context.Context, r *http.Request) (authInfo, bool) {
	info := authInfo{}
	if r.URL.Path == "/p/public" {
		info.Permissions = []string{"/p/public"}

		return info, true
	}

	return info, false
}

// authInfo describes what is passed between middlewares as result of authentication.
type authInfo struct {
	PrimaryIdentity string
	Permissions     []string `mapstructure:"permissions"`
}

// ctxKey scopes the context information.
type ctxKey string

func infoFromContext(ctx context.Context) authInfo {
	val, ok := ctx.Value(ctxKey("info")).(authInfo)
	if !ok {
		panic("stdcrpcaccess: no auth info in context")
	}

	return val
}
