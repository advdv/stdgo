package stdhttpware_test

import (
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
	chain := stdhttpware.Apply(base, zap.New(core))
	return logs, chain
}

func TestCacheMiddleware(t *testing.T) {
	t.Parallel()

	t.Run("sets default header when absent", func(t *testing.T) {
		t.Parallel()

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)

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
		req := httptest.NewRequest(http.MethodGet, "/", nil)

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
		req := httptest.NewRequest(http.MethodPost, "/foo", nil)

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
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		req.Header.Set("User-Agent", "ELB-HealthChecker/2.0")
		req.RemoteAddr = "10.0.0.12:1234"

		chain.ServeHTTP(rec, req)

		require.Equal(t, 0, logs.FilterMessage("request").Len())
	})
}

func TestRecoverMiddleware(t *testing.T) {
	t.Parallel()

	t.Run("recovers from panic", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("boom")
		panicHandler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(boom) })

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)

		logs, chain := setup(panicHandler)
		chain.ServeHTTP(rec, req)

		require.Equal(t, http.StatusInternalServerError, rec.Code)
		require.GreaterOrEqual(t, logs.FilterMessage("panic while serving request").Len(), 1)
	})

	t.Run("upgrade connection not forced to 500", func(t *testing.T) {
		t.Parallel()
		panicHandler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("upgrade") })

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	chain.ServeHTTP(rec, req)

	require.NotEmpty(t, handler.sawReqID)
	require.Equal(t, handler.sawReqID, rec.Header().Get("Sd-Request-Id"))
}
