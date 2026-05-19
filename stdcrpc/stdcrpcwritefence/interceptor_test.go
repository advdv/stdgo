package stdcrpcwritefence_test

// Black-box tests for [stdcrpcwritefence.Interceptor]. The interceptor's
// only job is to flip the package-internal fence-intent flag based
// on the inbound procedure's [connect.IdempotencyLevel], so the
// tests drive a real Connect handler through a real HTTP server and
// observe the issued Set-Cookie header on the response. Using a
// real Connect setup (rather than fabricating connect.AnyRequest)
// is the only way to exercise the IdempotencyLevel branch — the
// framework is the one that sets it on req.Spec().

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/advdv/stdgo/stdcrpc/stdcrpcwritefence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
)

const testProcedure = "/test.v1.TestService/Test"

// newFenceTestServer builds an httptest.Server that runs a single
// unary Connect handler at testProcedure, wrapped (innermost to
// outermost) by:
//   - the stdcrpcwritefence.Interceptor (server-side),
//   - the stdcrpcwritefence.Middleware (HTTP, owns the cookie).
//
// idem controls the procedure's declared idempotency level so the
// test can assert the interceptor's branch behaviour.
// returnErr, when non-nil, is returned by the handler — used to
// assert the interceptor does NOT pin on failure.
func newFenceTestServer(t *testing.T, idem connect.IdempotencyLevel, returnErr error) *httptest.Server {
	t.Helper()

	handler := connect.NewUnaryHandler(
		testProcedure,
		func(_ context.Context, _ *connect.Request[emptypb.Empty]) (*connect.Response[emptypb.Empty], error) {
			if returnErr != nil {
				return nil, returnErr
			}

			return connect.NewResponse(&emptypb.Empty{}), nil
		},
		connect.WithIdempotency(idem),
		connect.WithInterceptors(stdcrpcwritefence.Interceptor()),
	)

	mux := http.NewServeMux()
	mux.Handle(testProcedure, handler)

	srv := httptest.NewServer(promotedMiddleware()(mux))
	t.Cleanup(srv.Close)

	return srv
}

// callOnce dials testProcedure on srv using a real Connect client
// and returns the raw HTTP response so tests can inspect the
// Set-Cookie header.
func callOnce(
	t *testing.T, srv *httptest.Server, idem connect.IdempotencyLevel,
) (*connect.Response[emptypb.Empty], error) {
	t.Helper()

	client := connect.NewClient[emptypb.Empty, emptypb.Empty](
		srv.Client(),
		srv.URL+testProcedure,
		connect.WithIdempotency(idem),
	)

	return client.CallUnary(t.Context(), connect.NewRequest(&emptypb.Empty{}))
}

// cookieFromResponse returns the write-fence cookie set on the
// Connect response's header, or nil if none.
func cookieFromResponse(resp *connect.Response[emptypb.Empty]) *http.Cookie {
	if resp == nil {
		return nil
	}

	header := http.Header(resp.Header())
	// http.Response.Cookies() needs an *http.Response; emulate by
	// constructing one with just the headers — Cookies only reads
	// Set-Cookie.
	rr := &http.Response{Header: header} //nolint:exhaustruct

	for _, c := range rr.Cookies() {
		if c.Name == stdcrpcwritefence.DefaultCookieName {
			return c
		}
	}

	return nil
}

func TestInterceptor_unspecified_idempotency_pins(t *testing.T) {
	t.Parallel()

	// IDEMPOTENCY_UNKNOWN (proto-default) MUST pin — fail-safe: any
	// procedure that didn't declare its idempotency is assumed to
	// have side effects. Authors of pure reads opt out by marking
	// their methods NO_SIDE_EFFECTS.
	srv := newFenceTestServer(t, connect.IdempotencyUnknown, nil)

	resp, err := callOnce(t, srv, connect.IdempotencyUnknown)
	require.NoError(t, err)

	assert.NotNil(t, cookieFromResponse(resp),
		"IDEMPOTENCY_UNKNOWN must pin the cookie on success")
}

func TestInterceptor_idempotent_pins(t *testing.T) {
	t.Parallel()

	// IDEMPOTENT is "safe to retry" but still has side effects
	// (e.g. an idempotent write). MUST pin.
	srv := newFenceTestServer(t, connect.IdempotencyIdempotent, nil)

	resp, err := callOnce(t, srv, connect.IdempotencyIdempotent)
	require.NoError(t, err)

	assert.NotNil(t, cookieFromResponse(resp),
		"IDEMPOTENT must pin the cookie on success")
}

func TestInterceptor_no_side_effects_does_not_pin(t *testing.T) {
	t.Parallel()

	// NO_SIDE_EFFECTS is the explicit "pure read" declaration. MUST
	// NOT pin — that is the only way the read path stays cookie-free
	// when an annotated reader is added.
	srv := newFenceTestServer(t, connect.IdempotencyNoSideEffects, nil)

	resp, err := callOnce(t, srv, connect.IdempotencyNoSideEffects)
	require.NoError(t, err)

	assert.Nil(t, cookieFromResponse(resp),
		"NO_SIDE_EFFECTS must NOT pin the cookie")
}

func TestInterceptor_error_response_does_not_pin(t *testing.T) {
	t.Parallel()

	// A handler that returns an error MUST NOT pin — the cookie
	// promises a write happened, which is not the case if the call
	// failed. Use IDEMPOTENT so the interceptor is otherwise willing
	// to fence.
	srv := newFenceTestServer(t, connect.IdempotencyIdempotent,
		connect.NewError(connect.CodeInternal, errors.New("boom")))

	_, err := callOnce(t, srv, connect.IdempotencyIdempotent)
	require.Error(t, err, "handler returned an error; client must see it")

	// The error path returns no *connect.Response, so we read the
	// Set-Cookie off the underlying HTTP response by re-issuing the
	// call against the bare HTTP endpoint.
	httpReq, mkErr := http.NewRequestWithContext(t.Context(),
		http.MethodPost, srv.URL+testProcedure, http.NoBody)
	require.NoError(t, mkErr)

	httpReq.Header.Set("Content-Type", "application/proto")

	httpResp, httpErr := srv.Client().Do(httpReq)
	require.NoError(t, httpErr)
	t.Cleanup(func() { _ = httpResp.Body.Close() })

	for _, c := range httpResp.Cookies() {
		assert.NotEqual(t, stdcrpcwritefence.DefaultCookieName, c.Name,
			"a failed handler MUST NOT pin the fence cookie")
	}
}

func TestInterceptor_round_trip_promotes_routing(t *testing.T) {
	t.Parallel()

	// End-to-end through real Connect: an IDEMPOTENT call pins a
	// cookie; replaying that cookie on a follow-up call stamps
	// stdent.WithReadPromotion on the handler ctx. We probe the
	// stamp via a sibling HTTP route on the same test server so the
	// cookie's Path / Domain / SameSite defaults all participate.
	probe := &readPromotionProbe{}

	connectHandler := connect.NewUnaryHandler(
		testProcedure,
		func(_ context.Context, _ *connect.Request[emptypb.Empty]) (*connect.Response[emptypb.Empty], error) {
			return connect.NewResponse(&emptypb.Empty{}), nil
		},
		connect.WithIdempotency(connect.IdempotencyIdempotent),
		connect.WithInterceptors(stdcrpcwritefence.Interceptor()),
	)

	mux := http.NewServeMux()
	mux.Handle(testProcedure, connectHandler)
	mux.Handle("/probe", probe)

	srv := httptest.NewServer(promotedMiddleware()(mux))
	t.Cleanup(srv.Close)

	// 1) IDEMPOTENT call pins a cookie.
	client := connect.NewClient[emptypb.Empty, emptypb.Empty](
		srv.Client(),
		srv.URL+testProcedure,
		connect.WithIdempotency(connect.IdempotencyIdempotent),
	)

	resp, err := client.CallUnary(t.Context(), connect.NewRequest(&emptypb.Empty{}))
	require.NoError(t, err)

	issued := cookieFromResponse(resp)
	require.NotNil(t, issued, "IDEMPOTENT call must pin a cookie")

	// 2) Follow-up read WITHOUT the cookie must not be promoted.
	probe.got.Store(false)

	noCookieReq, mkErr := http.NewRequestWithContext(t.Context(),
		http.MethodGet, srv.URL+"/probe", http.NoBody)
	require.NoError(t, mkErr)

	noCookieResp, doErr := srv.Client().Do(noCookieReq)
	require.NoError(t, doErr)
	_ = noCookieResp.Body.Close()
	assert.False(t, probe.got.Load(),
		"unpinned read must NOT be promoted")

	// 3) Follow-up read WITH the cookie must be promoted.
	probe.got.Store(false)

	cookieReq, mkErr := http.NewRequestWithContext(t.Context(),
		http.MethodGet, srv.URL+"/probe", http.NoBody)
	require.NoError(t, mkErr)
	cookieReq.AddCookie(issued)

	cookieResp, doErr := srv.Client().Do(cookieReq)
	require.NoError(t, doErr)
	_ = cookieResp.Body.Close()
	assert.True(t, probe.got.Load(),
		"a valid, unexpired write-fence cookie MUST promote the follow-up read")
}
