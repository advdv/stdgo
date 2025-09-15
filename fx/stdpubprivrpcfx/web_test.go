package stdpubprivrpcfx_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	foov1 "github.com/advdv/stdgo/fx/stdpubprivrpcfx/internal/foo/v1"
	"github.com/advdv/stdgo/fx/stdpubprivrpcfx/internal/foo/v1/foov1connect"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/protobuf/proto"
)

const testRpcBasePath = "/xr"

func TestSetup(t *testing.T) {
	t.Parallel()
	ctx, pubh, privh, ro, rw, sys := setupAll(t)
	require.NotNil(t, ctx)
	require.NotNil(t, pubh)
	require.NotNil(t, privh)
	require.NotNil(t, ro)
	require.NotNil(t, rw)
	require.NotNil(t, sys)
}

func TestRPCMounts(t *testing.T) {
	t.Parallel()
	ctx, pubh, privh, _, _, _ := setupAll(t)

	req, resp := httptest.NewRequestWithContext(
		ctx, http.MethodGet, testRpcBasePath+foov1connect.ReadOnlyServiceWhoAmIProcedure, nil),
		httptest.NewRecorder()
	pubh.ServeHTTP(resp, req)
	require.Equal(t, 405, resp.Code)

	req, resp = httptest.NewRequestWithContext(
		ctx, http.MethodGet, testRpcBasePath+foov1connect.SystemServiceInitOrganizationProcedure, nil),
		httptest.NewRecorder()
	privh.ServeHTTP(resp, req)
	require.Equal(t, 405, resp.Code)
}

func TestHealthz(t *testing.T) {
	t.Parallel()
	var obs *observer.ObservedLogs
	_, pubh, privh, _, _, _ := setupAll(t, &obs)

	rec, req := httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-Id", "2201419daf154fb4acd2000000000009")
	pubh.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "OK\n", rec.Body.String())
	require.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))

	logEntries := obs.FilterMessage("request").All()
	require.Len(t, logEntries, 1)

	require.Equal(t, "2201419daf154fb4acd2000000000009", logEntries[0].ContextMap()["request_id"])

	rec, req = httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil)
	privh.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestHealthzFail(t *testing.T) {
	t.Parallel()
	var obs *observer.ObservedLogs
	_, pubh, _, _, _, _ := setupAll(t, &obs)

	rec, req := httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz?failhc=true", nil)
	pubh.ServeHTTP(rec, req)
	require.Equal(t, http.StatusPreconditionFailed, rec.Code)
}

func TestHealthzPanic(t *testing.T) {
	t.Parallel()
	var obs *observer.ObservedLogs
	_, pubh, _, _, _, _ := setupAll(t, &obs)

	rec, req := httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz?force_panic=true", nil)
	req.Header.Set("X-Request-Id", "2201419daf154fb4acd2000000000009")
	pubh.ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	logEntries := obs.FilterMessage("panic while serving request").All()
	require.Equal(t, "2201419daf154fb4acd2000000000009", logEntries[0].ContextMap()["request_header"].(http.Header)["X-Request-Id"][0])
	require.Len(t, logEntries, 1)
}

func TestRPCCalling(t *testing.T) {
	t.Parallel()
	t.Run("request validation", func(t *testing.T) {
		t.Parallel()

		ctx, _, _, ro, _, _ := setupAll(t)
		_, err := ro.WhoAmI(ctx, connect.NewRequest(foov1.WhoAmIRequest_builder{}.Build()))
		require.ErrorContains(t, err, "echo: value is required")
	})

	t.Run("response validation", func(t *testing.T) {
		t.Parallel()
		ctx, _, _, ro, _, _ := setupAll(t)
		_, err := ro.WhoAmI(ctx, connect.NewRequest(foov1.WhoAmIRequest_builder{Echo: proto.String("")}.Build()))
		require.ErrorContains(t, err, "response: validation error")
	})

	t.Run("ok", func(t *testing.T) {
		t.Parallel()
		ctx, _, _, ro, _, _ := setupAll(t)
		resp, err := ro.WhoAmI(ctx, connect.NewRequest(foov1.WhoAmIRequest_builder{Echo: proto.String("abc")}.Build()))
		require.NoError(t, err)
		require.NotNil(t, resp.Msg)
	})
}

func TestLambdaRelayin(t *testing.T) {
	t.Parallel()

	t.Run("ok", func(t *testing.T) {
		t.Parallel()

		var obs *observer.ObservedLogs
		ctx, _, privh, _, _, _ := setupAll(t, &obs)

		data, err := json.Marshal(events.S3Event{})
		require.NoError(t, err)

		resp, req := httptest.NewRecorder(),
			httptest.NewRequestWithContext(ctx, http.MethodPut, "/lambda/foo-relay-1", bytes.NewReader(data))
		privh.ServeHTTP(resp, req)

		require.Equal(t, http.StatusOK, resp.Result().StatusCode)
		require.JSONEq(t, `{}`, resp.Body.String())

		require.Equal(t, 1, obs.FilterMessage("init organization").Len())
	})
	t.Run("get", func(t *testing.T) {
		t.Parallel()
		ctx, _, privh, _, _, _ := setupAll(t)

		resp, req := httptest.NewRecorder(),
			httptest.NewRequestWithContext(ctx, http.MethodGet, "/lambda/foo-relay-1", nil)
		privh.ServeHTTP(resp, req)
		require.Equal(t, http.StatusBadRequest, resp.Result().StatusCode)
		require.Equal(t, `{"message":"Bad Request: no request body"}`+"\n", resp.Body.String())
	})
}
