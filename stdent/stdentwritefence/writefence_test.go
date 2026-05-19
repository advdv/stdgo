package stdentwritefence_test

// Black-box tests for the [stdentwritefence.Middleware]. The tests
// drive the middleware through Go's [httptest] machinery and exercise
// the write-observer side using [stdent.Transact0] against an
// in-memory fake transactor — i.e. the exact production code path,
// minus the actual database. The middleware's contract has two
// halves and the tests are organised accordingly:
//
//   - Read side: an incoming cookie is verified by securecookie; on
//     success ctx is stamped with stdent.WithReadPromotion (observed
//     here via stdent.HasReadPromotion since the middleware's own
//     ctx mutation is otherwise unobservable from outside).
//
//   - Write side: stdent.WithWriteObserver is installed on the
//     request ctx, then any stdent.Transact* call against a non-read-
//     only transactor inside the handler flips the observer; the
//     middleware pins a fresh signed cookie on the response.
//
// The fake transactor types live next to the test so the file is
// self-contained and does not pull in the real stdent transact tests.

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdent"
	"github.com/advdv/stdgo/stdent/stdentwritefence"
	"github.com/gorilla/securecookie"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// 32 bytes — minimum accepted by the middleware.
var testHashKey = []byte("0123456789abcdef0123456789abcdef")

// fakeTx is a minimal [stdent.Tx] used to drive [stdent.Transact1]
// from inside a test handler. Commit / Rollback are no-ops; the
// observer flip happens in stdent.Transact1's commit path.
type fakeTx struct{}

func (fakeTx) Commit() error   { return nil }
func (fakeTx) Rollback() error { return sql.ErrTxDone }

// fakeClient is a stand-in [stdent.Client] returning fakeTx values.
type fakeClient struct{ calls atomic.Int64 }

func (c *fakeClient) BeginTx(context.Context, *entsql.TxOptions) (fakeTx, error) {
	c.calls.Add(1)

	return fakeTx{}, nil
}

// newRW returns a fresh non-read-only Transactor used to trip the
// observer inside test handlers.
func newRW() *stdent.Transactor[fakeTx] {
	return stdent.New[fakeTx](&fakeClient{})
}

// withLogger attaches the no-op zap logger expected by stdent.Transact1.
func withLogger(r *http.Request) *http.Request {
	return r.WithContext(stdctx.WithLogger(r.Context(), zap.NewNop()))
}

// readPromotionProbe is a [http.Handler] that records whether
// [stdent.HasReadPromotion] is true on the inbound ctx. Used to
// observe the read side of the middleware end-to-end without
// peeking at the unexported ctx key.
type readPromotionProbe struct {
	got atomic.Bool
}

func (p *readPromotionProbe) ServeHTTP(_ http.ResponseWriter, r *http.Request) {
	p.got.Store(stdent.HasReadPromotion(r.Context()))
}

// findSetCookie returns the parsed Set-Cookie header whose Name
// matches name, or nil if none. The middleware always issues at most
// one cookie, so this is unambiguous in tests.
func findSetCookie(t *testing.T, resp *http.Response, name string) *http.Cookie {
	t.Helper()

	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}

	return nil
}

func TestMiddleware_no_cookie_no_promotion(t *testing.T) {
	t.Parallel()

	// A request with no write-fence cookie must NOT be stamped with
	// stdent.WithReadPromotion. This is the baseline routing decision
	// the rest of the system depends on.
	probe := &readPromotionProbe{}
	mw := stdentwritefence.Middleware(testHashKey)(probe)

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, withLogger(httptest.NewRequest(http.MethodGet, "/x", nil)))

	assert.False(t, probe.got.Load(), "missing cookie MUST NOT promote reads")
	assert.Empty(t, rr.Result().Cookies(), "no observed write means no Set-Cookie")
}

func TestMiddleware_valid_cookie_promotes_read(t *testing.T) {
	t.Parallel()

	// A request that carries a cookie signed by THIS middleware's
	// key MUST be stamped with stdent.WithReadPromotion so the
	// stdent.TransactR rule can route it to rw.
	probe := &readPromotionProbe{}
	mw := stdentwritefence.Middleware(testHashKey)(probe)

	// Generate a valid cookie by running a request that observes a
	// write — that's the only public path that issues one and keeps
	// the test honest about the wire format.
	rwTxr := newRW()
	writerHandler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		assert.NoError(t, stdent.Transact0(r.Context(), rwTxr,
			func(context.Context, fakeTx) error { return nil }))
	})
	writeMW := stdentwritefence.Middleware(testHashKey)(writerHandler)

	wrr := httptest.NewRecorder()
	writeMW.ServeHTTP(wrr, withLogger(httptest.NewRequest(http.MethodPost, "/w", nil)))

	issued := findSetCookie(t, wrr.Result(), stdentwritefence.DefaultCookieName)
	require.NotNil(t, issued, "writer request must produce a Set-Cookie")

	// Replay the cookie on a follow-up read.
	req := withLogger(httptest.NewRequest(http.MethodGet, "/r", nil))
	req.AddCookie(issued)

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	assert.True(t, probe.got.Load(),
		"a valid, unexpired write-fence cookie MUST stamp WithReadPromotion on the handler ctx")
}

func TestMiddleware_tampered_cookie_no_promotion(t *testing.T) {
	t.Parallel()

	// A cookie whose signature doesn't verify (e.g. signed with a
	// DIFFERENT key) MUST NOT promote — otherwise the HMAC is
	// purely decorative.
	otherKey := []byte("ffffffffffffffffffffffffffffffff")
	sc := securecookie.New(otherKey, nil)
	sc.MaxAge(int(stdentwritefence.DefaultTTL.Seconds()))

	value, err := sc.Encode(stdentwritefence.DefaultCookieName, "1")
	require.NoError(t, err)

	probe := &readPromotionProbe{}
	mw := stdentwritefence.Middleware(testHashKey)(probe)

	req := withLogger(httptest.NewRequest(http.MethodGet, "/x", nil))
	req.AddCookie(&http.Cookie{Name: stdentwritefence.DefaultCookieName, Value: value})

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	assert.False(t, probe.got.Load(),
		"a cookie signed with a different key MUST NOT promote")
	assert.Empty(t, rr.Result().Cookies(),
		"bad cookies must NOT trigger a Set-Cookie on the response")
}

func TestMiddleware_garbage_cookie_no_promotion(t *testing.T) {
	t.Parallel()

	// A non-decodable cookie value must fail-open to "no promotion"
	// without crashing the request.
	probe := &readPromotionProbe{}
	mw := stdentwritefence.Middleware(testHashKey)(probe)

	req := withLogger(httptest.NewRequest(http.MethodGet, "/x", nil))
	req.AddCookie(&http.Cookie{Name: stdentwritefence.DefaultCookieName, Value: "not-a-valid-securecookie"})

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	assert.False(t, probe.got.Load(), "garbage cookies must NOT promote")
}

func TestMiddleware_expired_cookie_no_promotion(t *testing.T) {
	t.Parallel()

	// A cookie older than the TTL must be rejected by securecookie's
	// MaxAge check. We synthesise an expired cookie by encoding with
	// a codec whose MaxAge is very short and then waiting past it.
	mw := stdentwritefence.Middleware(testHashKey, stdentwritefence.WithTTL(time.Second))

	// Match the middleware's codec config so the cookie is otherwise
	// indistinguishable from a real one.
	sc := securecookie.New(testHashKey, nil)
	sc.MaxAge(1)

	value, err := sc.Encode(stdentwritefence.DefaultCookieName, "1")
	require.NoError(t, err)

	// Wait past the MaxAge so securecookie's internal timestamp
	// check rejects the cookie. Two seconds gives us a comfortable
	// margin past the 1s MaxAge in CI environments.
	time.Sleep(2 * time.Second)

	probe := &readPromotionProbe{}
	handler := mw(probe)

	req := withLogger(httptest.NewRequest(http.MethodGet, "/x", nil))
	req.AddCookie(&http.Cookie{Name: stdentwritefence.DefaultCookieName, Value: value})

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.False(t, probe.got.Load(), "expired cookies MUST NOT promote")
}

func TestMiddleware_observed_write_sets_cookie(t *testing.T) {
	t.Parallel()

	// A handler that successfully commits a non-read-only stdent
	// transaction MUST cause the middleware to add a Set-Cookie
	// header for the configured cookie. This is the round-trip the
	// whole feature depends on.
	rwTxr := newRW()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, stdent.Transact0(r.Context(), rwTxr,
			func(context.Context, fakeTx) error { return nil }))
		w.WriteHeader(http.StatusNoContent)
	})

	mw := stdentwritefence.Middleware(testHashKey)(handler)

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, withLogger(httptest.NewRequest(http.MethodPost, "/w", nil)))

	require.Equal(t, http.StatusNoContent, rr.Result().StatusCode)

	c := findSetCookie(t, rr.Result(), stdentwritefence.DefaultCookieName)
	require.NotNil(t, c, "an observed write MUST emit a Set-Cookie")
	assert.NotEmpty(t, c.Value, "cookie value must be non-empty")
	assert.Equal(t, "/", c.Path, "default cookie Path must be /")
	assert.True(t, c.HttpOnly, "cookie must be HttpOnly")
	assert.True(t, c.Secure, "cookie must be Secure by default")
	assert.Equal(t, http.SameSiteLaxMode, c.SameSite, "default SameSite must be Lax")
	assert.Equal(t, int(stdentwritefence.DefaultTTL.Seconds()), c.MaxAge,
		"cookie MaxAge must equal the configured TTL in seconds")
}

func TestMiddleware_no_observed_write_no_cookie(t *testing.T) {
	t.Parallel()

	// A handler that performs no writes (or only reads through a
	// read-only transactor) MUST NOT cause a Set-Cookie. The
	// observer mechanism is the sole trigger — there is no implicit
	// "POST always pins" rule, by design.
	roTxr := stdent.New(&fakeClient{}, stdent.ReadOnly(true))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, stdent.Transact0(r.Context(), roTxr,
			func(context.Context, fakeTx) error { return nil }))
		w.WriteHeader(http.StatusOK)
	})

	mw := stdentwritefence.Middleware(testHashKey)(handler)

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, withLogger(httptest.NewRequest(http.MethodPost, "/r", nil)))

	require.Nil(t, findSetCookie(t, rr.Result(), stdentwritefence.DefaultCookieName),
		"read-only commit MUST NOT pin the cookie")
}

func TestMiddleware_handler_returns_without_writing(t *testing.T) {
	t.Parallel()

	// A handler that commits a write but never calls WriteHeader /
	// Write must still get the cookie set — the middleware covers
	// this case by re-running setCookie after next.ServeHTTP.
	rwTxr := newRW()

	handler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		assert.NoError(t, stdent.Transact0(r.Context(), rwTxr,
			func(context.Context, fakeTx) error { return nil }))
		// no explicit WriteHeader / Write — net/http will emit 200.
	})

	mw := stdentwritefence.Middleware(testHashKey)(handler)

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, withLogger(httptest.NewRequest(http.MethodPost, "/w", nil)))

	require.NotNil(t, findSetCookie(t, rr.Result(), stdentwritefence.DefaultCookieName),
		"observed write must pin cookie even when handler returns without writing")
}

func TestMiddleware_cookie_set_before_writeheader_flush(t *testing.T) {
	t.Parallel()

	// The cookie must be present on the response headers OBSERVED by
	// the client — i.e. it must be added before WriteHeader flushes.
	// Calling WriteHeader explicitly inside the handler is the
	// scenario most likely to expose an ordering bug.
	rwTxr := newRW()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, stdent.Transact0(r.Context(), rwTxr,
			func(context.Context, fakeTx) error { return nil }))
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	})

	mw := stdentwritefence.Middleware(testHashKey)(handler)

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, withLogger(httptest.NewRequest(http.MethodPost, "/w", nil)))

	res := rr.Result()
	require.Equal(t, http.StatusCreated, res.StatusCode)
	require.NotNil(t, findSetCookie(t, res, stdentwritefence.DefaultCookieName),
		"cookie must be added before WriteHeader flushes")
}

func TestMiddleware_options_honored(t *testing.T) {
	t.Parallel()

	// All options the user can twist must reach the issued cookie.
	const (
		name   = "myapp_wf"
		path   = "/api"
		domain = "example.test"
		ttl    = 7 * time.Second
	)

	rwTxr := newRW()
	handler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		assert.NoError(t, stdent.Transact0(r.Context(), rwTxr,
			func(context.Context, fakeTx) error { return nil }))
	})

	mw := stdentwritefence.Middleware(testHashKey,
		stdentwritefence.WithCookieName(name),
		stdentwritefence.WithPath(path),
		stdentwritefence.WithDomain(domain),
		stdentwritefence.WithSameSite(http.SameSiteStrictMode),
		stdentwritefence.WithInsecure(),
		stdentwritefence.WithTTL(ttl),
	)(handler)

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, withLogger(httptest.NewRequest(http.MethodPost, "/w", nil)))

	c := findSetCookie(t, rr.Result(), name)
	require.NotNil(t, c, "options must not break cookie issuance")
	assert.Equal(t, name, c.Name)
	assert.Equal(t, path, c.Path)
	assert.Equal(t, domain, c.Domain)
	assert.Equal(t, http.SameSiteStrictMode, c.SameSite)
	assert.False(t, c.Secure, "WithInsecure must drop the Secure attribute")
	assert.Equal(t, int(ttl.Seconds()), c.MaxAge)
}

func TestMiddleware_cookie_name_isolation(t *testing.T) {
	t.Parallel()

	// A middleware configured with a custom cookie name MUST NOT
	// honour cookies under the DEFAULT name (defence against two
	// apps sharing a domain accidentally cross-promoting).
	rwTxr := newRW()
	writerHandler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		assert.NoError(t, stdent.Transact0(r.Context(), rwTxr,
			func(context.Context, fakeTx) error { return nil }))
	})

	// Issue under the default name.
	defaultMW := stdentwritefence.Middleware(testHashKey)(writerHandler)

	wrr := httptest.NewRecorder()
	defaultMW.ServeHTTP(wrr, withLogger(httptest.NewRequest(http.MethodPost, "/w", nil)))

	issued := findSetCookie(t, wrr.Result(), stdentwritefence.DefaultCookieName)
	require.NotNil(t, issued)

	// Replay against a middleware configured to look for a DIFFERENT
	// cookie name — it must not promote.
	probe := &readPromotionProbe{}
	customMW := stdentwritefence.Middleware(testHashKey, stdentwritefence.WithCookieName("other"))(probe)

	req := withLogger(httptest.NewRequest(http.MethodGet, "/r", nil))
	req.AddCookie(issued)

	rrr := httptest.NewRecorder()
	customMW.ServeHTTP(rrr, req)

	assert.False(t, probe.got.Load(),
		"a middleware looking for a different cookie name MUST ignore the default-named cookie")
}

func TestMiddleware_round_trip_promotes_routing(t *testing.T) {
	t.Parallel()

	// End-to-end: first request writes (and pins the cookie), second
	// request replays the cookie and observes that stdent.TransactR
	// against the same ro/rw pair routes the read to the rw pool.
	roC := &fakeClient{}
	rwC := &fakeClient{}
	ro := stdent.New(roC, stdent.ReadOnly(true))
	rw := stdent.New(rwC)

	writeHandler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		assert.NoError(t, stdent.Transact0(r.Context(), rw,
			func(context.Context, fakeTx) error { return nil }))
	})

	readHandler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, err := stdent.TransactR(r.Context(), ro, rw, &struct{}{},
			func(context.Context, fakeTx, *struct{}) (*struct{}, error) {
				return &struct{}{}, nil
			})
		assert.NoError(t, err)
	})

	mw := stdentwritefence.Middleware(testHashKey)

	// 1) write request → cookie issued, rwC sees one BeginTx
	wrr := httptest.NewRecorder()
	mw(writeHandler).ServeHTTP(wrr, withLogger(httptest.NewRequest(http.MethodPost, "/w", nil)))

	issued := findSetCookie(t, wrr.Result(), stdentwritefence.DefaultCookieName)
	require.NotNil(t, issued, "write request must pin a cookie")
	require.Equal(t, int64(1), rwC.calls.Load(), "write should drive rw exactly once")
	require.Equal(t, int64(0), roC.calls.Load(), "write must not touch ro")

	// 2) read request without cookie → ro
	rrr1 := httptest.NewRecorder()
	mw(readHandler).ServeHTTP(rrr1, withLogger(httptest.NewRequest(http.MethodGet, "/r", nil)))
	require.Equal(t, int64(1), roC.calls.Load(), "unpinned read must route to ro")
	require.Equal(t, int64(1), rwC.calls.Load(), "unpinned read must NOT touch rw")

	// 3) read request WITH cookie → rw (read promotion)
	req := withLogger(httptest.NewRequest(http.MethodGet, "/r", nil))
	req.AddCookie(issued)

	rrr2 := httptest.NewRecorder()
	mw(readHandler).ServeHTTP(rrr2, req)
	require.Equal(t, int64(1), roC.calls.Load(),
		"pinned read MUST NOT touch ro")
	require.Equal(t, int64(2), rwC.calls.Load(),
		"pinned read MUST be promoted to rw")
}

func TestMiddleware_panic_on_short_key(t *testing.T) {
	t.Parallel()

	// A short hashKey is a programmer error — better to fail loudly
	// at construction time than ship a weakly-signed cookie.
	defer func() {
		r := recover()
		require.NotNil(t, r, "short hashKey MUST panic")

		msg, _ := r.(string)
		assert.Contains(t, msg, "hashKey", "panic message should mention hashKey")
	}()

	_ = stdentwritefence.Middleware([]byte("too-short"))
}

func TestMiddleware_does_not_write_on_read_path(t *testing.T) {
	t.Parallel()

	// On any read path (no observed write), the middleware MUST NOT
	// touch the response — not even to clear an existing cookie. The
	// browser is the authority on cookie lifetime; the server only
	// pins, never unpins.
	probe := &readPromotionProbe{}
	mw := stdentwritefence.Middleware(testHashKey)(probe)

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, withLogger(httptest.NewRequest(http.MethodGet, "/x", nil)))

	res := rr.Result()
	assert.Empty(t, res.Cookies(), "no observed write means no Set-Cookie")
	// httptest.NewRecorder defaults to 200, but only because that's
	// the recorder's zero value. We assert nothing else was set.
	assert.Empty(t, res.Header.Get("Set-Cookie"),
		"middleware must not write any Set-Cookie on a pure read path")
}
