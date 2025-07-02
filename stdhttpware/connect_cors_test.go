package stdhttpware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdhttpware"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestCORS(t *testing.T) {
	t.Parallel()

	t.Run("not-allow", func(t *testing.T) {
		t.Parallel()

		mw := stdhttpware.NewConnectCORSMiddleware(10)
		ctx := stdctx.WithLogger(t.Context(), zap.NewNop())

		rec, req := httptest.NewRecorder(), httptest.NewRequestWithContext(ctx, http.MethodOptions, "/", nil)
		req.Header.Add("Access-Control-Request-Headers", "connect-protocol-version,content-type,cookie")
		req.Header.Set("Access-Control-Request-Method", "GET")
		req.Header.Set("Origin", "http://localhost:3030")

		mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(rec, req)

		require.Equal(t, http.StatusNoContent, rec.Result().StatusCode)
		require.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("allow-by-whitelist", func(t *testing.T) {
		t.Parallel()
		mw := stdhttpware.NewConnectCORSMiddleware(10, "http://localhost:3030")
		ctx := stdctx.WithLogger(t.Context(), zap.NewNop())

		rec, req := httptest.NewRecorder(), httptest.NewRequestWithContext(ctx, http.MethodOptions, "/", nil)
		req.Header.Add("Access-Control-Request-Headers", "connect-protocol-version,content-type,cookie")
		req.Header.Set("Access-Control-Request-Method", "GET")
		req.Header.Set("Origin", "http://localhost:3030")

		mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(rec, req)

		require.Equal(t, http.StatusNoContent, rec.Result().StatusCode)
		require.Equal(t, "http://localhost:3030", rec.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("allow-by-root", func(t *testing.T) {
		t.Parallel()
		mw := stdhttpware.NewConnectCORSMiddleware(10)
		ctx := stdctx.WithLogger(t.Context(), zap.NewNop())

		rec, req := httptest.NewRecorder(), httptest.NewRequestWithContext(ctx, http.MethodOptions, "/", nil)
		req.Header.Add("Access-Control-Request-Headers", "connect-protocol-version,content-type,cookie")
		req.Header.Set("Access-Control-Request-Method", "GET")
		req.Header.Set("Origin", "http://foo.bar.example.com")

		mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(rec, req)

		require.Equal(t, http.StatusNoContent, rec.Result().StatusCode)
		require.Equal(t, "http://foo.bar.example.com", rec.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("not-allow-invalid-origin", func(t *testing.T) {
		t.Parallel()

		zc, obs := observer.New(zap.DebugLevel)
		mw := stdhttpware.NewConnectCORSMiddleware(10)
		ctx := stdctx.WithLogger(t.Context(), zap.New(zc))

		rec, req := httptest.NewRecorder(), httptest.NewRequestWithContext(ctx, http.MethodOptions, "/", nil)
		req.Header.Add("Access-Control-Request-Headers", "connect-protocol-version,content-type,cookie")
		req.Header.Set("Access-Control-Request-Method", "GET")
		req.Header.Set("Origin", "http://example.com/%2")

		mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(rec, req)

		require.Equal(t, http.StatusNoContent, rec.Result().StatusCode)
		require.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))

		require.Len(t, obs.FilterMessage("invalid origin header received").All(), 1)
	})
}
