package stdhttpware_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdhttpware"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

type spyHandler struct {
	sawLogger *zap.Logger
	sawReqID  string
	mutate    func(w http.ResponseWriter)
}

func (h *spyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.mutate != nil {
		h.mutate(w)
	}
	h.sawLogger = stdctx.Log(r.Context())
	h.sawReqID = chimiddleware.GetReqID(r.Context())
	_, _ = w.Write([]byte("ok"))
}

func setup(base http.Handler) (*observer.ObservedLogs, http.Handler) {
	core, logs := observer.New(zapcore.DebugLevel)
	chain := stdhttpware.Apply(base, zap.New(core),
		[]string{"authorization", "Proxy-Authorization", "Cookie", "Set-Cookie"})
	return logs, chain
}

func TestCacheMiddleware(t *testing.T) {
	t.Parallel()

	t.Run("sets default header when absent", func(t *testing.T) {
		t.Parallel()

		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)

		_, chain := setup(&spyHandler{})
		chain.ServeHTTP(rec, req)

		require.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))
	})

	t.Run("preserves explicit cache header set by inner handler", func(t *testing.T) {
		t.Parallel()

		explicit := "public, max-age=3600"
		handler := &spyHandler{mutate: func(w http.ResponseWriter) {
			w.Header().Set("Cache-Control", explicit)
		}}

		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)

		_, chain := setup(handler)
		chain.ServeHTTP(rec, req)

		require.Equal(t, explicit, rec.Header().Get("Cache-Control"))
	})
}

func TestLoggerMiddleware(t *testing.T) {
	t.Parallel()

	t.Run("injects logger and request id", func(t *testing.T) {
		t.Parallel()
		handler := &spyHandler{}
		logs, chain := setup(handler)

		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/foo", nil)

		chain.ServeHTTP(rec, req)

		require.NotNil(t, handler.sawLogger)
		require.NotEmpty(t, rec.Header().Get("Sd-Request-Id"))
		require.Equal(t, 1, logs.FilterMessage("request").Len())
	})

	t.Run("suppresses health‑checker logs", func(t *testing.T) {
		t.Parallel()
		handler := &spyHandler{}
		logs, chain := setup(handler)

		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health", nil)
		req.Header.Set("User-Agent", "ELB-HealthChecker/2.0")
		req.RemoteAddr = "10.0.0.12:1234"

		chain.ServeHTTP(rec, req)

		require.Equal(t, 0, logs.FilterMessage("request").Len())
	})

	for _, tc := range []struct {
		desc       string
		userAgent  string
		remoteAddr string
		wantLogged bool
	}{
		{"alb from private address", "ELB-HealthChecker/2.0", "10.0.0.12:1234", false},
		{"alb from private address set by RealIP", "ELB-HealthChecker/2.0", "10.0.0.12", false},
		{"alb from public address", "ELB-HealthChecker/2.0", "203.0.113.7:1234", true},
		{"wget from loopback", "Wget", "127.0.0.1:1234", false},
		{"wget from ipv6 loopback", "Wget", "[::1]:1234", false},
		{"wget from private address", "Wget", "10.0.0.12:1234", true},
		{"browser from private address", "Mozilla/5.0", "10.0.0.12:1234", true},
		{"alb from unparsable address", "ELB-HealthChecker/2.0", "nope", true},
	} {
		t.Run("health check filter: "+tc.desc, func(t *testing.T) {
			t.Parallel()
			logs, chain := setup(&spyHandler{})

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health", nil)
			req.Header.Set("User-Agent", tc.userAgent)
			req.RemoteAddr = tc.remoteAddr

			chain.ServeHTTP(httptest.NewRecorder(), req)

			require.Equal(t, tc.wantLogged, logs.FilterMessage("request").Len() == 1)
		})
	}

	t.Run("redacts sensitive headers", func(t *testing.T) {
		t.Parallel()
		handler := &spyHandler{mutate: func(w http.ResponseWriter) {
			w.Header().Set("Set-Cookie", "session=secret")
		}}
		logs, chain := setup(handler)

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer secret")
		req.Header.Set("Cookie", "session=secret")
		req.Header.Set("Accept", "text/plain")

		chain.ServeHTTP(httptest.NewRecorder(), req)

		entries := logs.FilterMessage("request").All()
		require.Len(t, entries, 1)

		fields := entries[0].ContextMap()
		reqHeader, ok := fields["request_header"].(http.Header)
		require.True(t, ok)
		require.Equal(t, []string{"[REDACTED]"}, reqHeader.Values("Authorization"))
		require.Equal(t, []string{"[REDACTED]"}, reqHeader.Values("Cookie"))
		require.Equal(t, []string{"text/plain"}, reqHeader.Values("Accept"))

		respHeader, ok := fields["response_header"].(http.Header)
		require.True(t, ok)
		require.Equal(t, []string{"[REDACTED]"}, respHeader.Values("Set-Cookie"))

		require.Equal(t, "Bearer secret", req.Header.Get("Authorization"), "the request itself is unchanged")
	})
}

func TestRecoverMiddleware(t *testing.T) {
	t.Parallel()

	t.Run("recovers from panic", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("boom")
		panicHandler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(boom) })

		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)

		logs, chain := setup(panicHandler)
		chain.ServeHTTP(rec, req)

		require.Equal(t, http.StatusInternalServerError, rec.Code)
		require.GreaterOrEqual(t, logs.FilterMessage("panic while serving request").Len(), 1)
	})

	t.Run("redacts sensitive headers", func(t *testing.T) {
		t.Parallel()
		panicHandler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer secret")

		logs, chain := setup(panicHandler)
		chain.ServeHTTP(httptest.NewRecorder(), req)

		entries := logs.FilterMessage("panic while serving request").All()
		require.Len(t, entries, 1)

		reqHeader, ok := entries[0].ContextMap()["request_header"].(http.Header)
		require.True(t, ok)
		require.Equal(t, []string{"[REDACTED]"}, reqHeader.Values("Authorization"))
	})

	t.Run("upgrade connection not forced to 500", func(t *testing.T) {
		t.Parallel()
		panicHandler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("upgrade") })

		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		req.Header.Set("Connection", "Upgrade")

		_, chain := setup(panicHandler)
		chain.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
	})
}

func TestRequestIDPropagation(t *testing.T) {
	t.Parallel()

	handler := &spyHandler{}
	_, chain := setup(handler)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)

	chain.ServeHTTP(rec, req)

	require.NotEmpty(t, handler.sawReqID)
	require.Equal(t, handler.sawReqID, rec.Header().Get("Sd-Request-Id"))
}
