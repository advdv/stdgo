package stdcrpcaccess_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "embed"

	"github.com/advdv/stdgo/stdcrpc/stdcrpcaccess"
	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdlo"
	"github.com/lestrrat-go/jwx/v2/jwt/openid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func permissionToProcedure(s string, _ int) string { return s }

func TestCheckAuth(t *testing.T) {
	tsrv := stdcrpcaccess.FixedKeyServer()

	tok := openid.New()
	tok.Set("permissions", []string{"/a/b", "/x/y"})
	tok.Set("role", "some-role")
	validToken1, err := stdcrpcaccess.SignToken(tok)
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
			`{"permissions":["/a/b", "/x/y"],"role":"some-role"}`,
			func(h http.Header) {
				h.Set("X-Amzn-Oidc-Accesstoken", validToken1)
			},
			func(t *testing.T, obs *observer.ObservedLogs) {
				t.Helper()
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			core, obs := observer.New(zapcore.DebugLevel)
			logs := zap.New(core)

			ac := stdcrpcaccess.New(tsrv.URL, permissionToProcedure)
			rec, req := httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, tt.path, nil)
			tt.setHdr(req.Header)
			req = req.WithContext(stdctx.WithLogger(ctx, logs))

			ac.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{
					"permissions": stdcrpcaccess.PermissionsFromContext(r.Context()),
					"role":        stdcrpcaccess.RoleFromContext(r.Context()),
				})
			})).ServeHTTP(rec, req)

			require.Equal(t, tt.expCode, rec.Code)
			require.JSONEq(t, tt.expJSON, rec.Body.String())
			tt.assertLogs(t, obs)

			require.NoError(t, ac.Close(ctx))
		})
	}
}

func TestWithHTTPClient(t *testing.T) {
	tsrv := stdcrpcaccess.FixedKeyServer()
	ac := stdcrpcaccess.New(tsrv.URL, permissionToProcedure)
	zc, _ := observer.New(zap.DebugLevel)
	logs := zap.New(zc)

	innter := ac.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"permissions": stdcrpcaccess.PermissionsFromContext(r.Context()),
			"role":        stdcrpcaccess.RoleFromContext(r.Context()),
		})
	}))

	outer := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innter.ServeHTTP(w, r.WithContext(stdctx.WithLogger(r.Context(), logs)))
	}))

	srv := httptest.NewServer(outer)

	ctx := t.Context()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/a/b", nil)

	tok := openid.New()
	tok.Set("permissions", []string{"/a/b"})
	tok.Set("role", "some-role")

	cln := stdcrpcaccess.WithSignedToken(srv.Client(), func(r *http.Request) openid.Token { return tok })
	resp, err := cln.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })

	body := stdlo.Must1(io.ReadAll(resp.Body))
	require.JSONEq(t, `{"permissions":["/a/b"],"role":"some-role"}`, string(body))
	require.Equal(t, 200, resp.StatusCode)
}
