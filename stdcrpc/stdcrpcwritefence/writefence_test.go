package stdcrpcwritefence_test

// Black-box tests for [stdcrpcwritefence.Middleware]. The middleware's
// contract has two halves and the tests are organised accordingly:
//
//   - Read side: an incoming cookie is verified by securecookie; on
//     success ctx is stamped with stdent.WithReadPromotion (observed
//     here via stdent.HasReadPromotion since the middleware's own
//     ctx mutation is otherwise unobservable from outside).
//
//   - Write side: the middleware installs a fence-intent flag on
//     the request ctx; any caller down the stack (canonically
//     stdcrpcwritefence.Interceptor, here a test handler calling
//     stdcrpcwritefence.MarkFenceIntent directly) can flip the flag and
//     thereby request a cookie pin. The interceptor's own behaviour
//     is exercised in interceptor_test.go.
//
// Driving the write side through MarkFenceIntent (rather than
// through the interceptor + Connect machinery) keeps these tests
// focused on the middleware's response-side responsibilities:
// flag → cookie, cookie-before-flush, options honoured, no
// spurious writes on the read path.

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/advdv/stdgo/stdent"
	"github.com/advdv/stdgo/stdcrpc/stdcrpcwritefence"
	"github.com/gorilla/securecookie"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 32 bytes — minimum accepted by the middleware.
var testHashKey = []byte("0123456789abcdef0123456789abcdef")

// promotedMiddleware wraps the package's [stdcrpcwritefence.Middleware]
// with the [stdent.WithReadPromotion] promoter pre-wired, so tests
// exercise the same end-to-end routing behaviour that production
// composition roots wire up via [stdcrpcwritefence.WithReadPromotion].
// Passing the promoter explicitly here also keeps the
// stdcrpcwritefence package free of any compile-time dependency on
// stdent — the seam is asserted in test, not in production code.
func promotedMiddleware(opts ...stdcrpcwritefence.Option) func(http.Handler) http.Handler {
	opts = append([]stdcrpcwritefence.Option{
		stdcrpcwritefence.WithReadPromotion(stdent.WithReadPromotion),
	}, opts...)

	return stdcrpcwritefence.Middleware(testHashKey, opts...)
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

// markingHandler returns an http.HandlerFunc that flips the
// fence-intent flag and then runs body. Stand-in for what
// [stdcrpcwritefence.Interceptor] (or any other caller that knows it
// wrote) would do in production.
func markingHandler(body func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stdcrpcwritefence.MarkFenceIntent(r.Context())

		if body != nil {
			body(w, r)
		}
	}
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
	mw := promotedMiddleware()(probe)

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/x", nil))

	assert.False(t, probe.got.Load(), "missing cookie MUST NOT promote reads")
	assert.Empty(t, rr.Result().Cookies(), "no observed write means no Set-Cookie")
}

func TestMiddleware_valid_cookie_promotes_read(t *testing.T) {
	t.Parallel()

	// A request that carries a cookie signed by THIS middleware's
	// key MUST be stamped with stdent.WithReadPromotion so the
	// stdent.TransactR rule can route it to rw.
	probe := &readPromotionProbe{}
	mw := promotedMiddleware()(probe)

	// Generate a valid cookie by running a request that flips the
	// fence-intent flag — that's the only public path that issues
	// one and keeps the test honest about the wire format.
	writeMW := promotedMiddleware()(markingHandler(nil))

	wrr := httptest.NewRecorder()
	writeMW.ServeHTTP(wrr, httptest.NewRequest(http.MethodPost, "/w", nil))

	issued := findSetCookie(t, wrr.Result(), stdcrpcwritefence.DefaultCookieName)
	require.NotNil(t, issued, "writer request must produce a Set-Cookie")

	// Replay the cookie on a follow-up read.
	req := httptest.NewRequest(http.MethodGet, "/r", nil)
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
	sc.MaxAge(int(stdcrpcwritefence.DefaultTTL.Seconds()))

	value, err := sc.Encode(stdcrpcwritefence.DefaultCookieName, "1")
	require.NoError(t, err)

	probe := &readPromotionProbe{}
	mw := promotedMiddleware()(probe)

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.AddCookie(&http.Cookie{Name: stdcrpcwritefence.DefaultCookieName, Value: value})

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
	mw := promotedMiddleware()(probe)

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.AddCookie(&http.Cookie{Name: stdcrpcwritefence.DefaultCookieName, Value: "not-a-valid-securecookie"})

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	assert.False(t, probe.got.Load(), "garbage cookies must NOT promote")
}

func TestMiddleware_expired_cookie_no_promotion(t *testing.T) {
	t.Parallel()

	// A cookie older than the TTL must be rejected by securecookie's
	// MaxAge check. We synthesise an expired cookie by encoding with
	// a codec whose MaxAge is very short and then waiting past it.
	mw := promotedMiddleware(stdcrpcwritefence.WithTTL(time.Second))

	// Match the middleware's codec config so the cookie is otherwise
	// indistinguishable from a real one.
	sc := securecookie.New(testHashKey, nil)
	sc.MaxAge(1)

	value, err := sc.Encode(stdcrpcwritefence.DefaultCookieName, "1")
	require.NoError(t, err)

	// Wait past the MaxAge so securecookie's internal timestamp
	// check rejects the cookie. Two seconds gives us a comfortable
	// margin past the 1s MaxAge in CI environments.
	time.Sleep(2 * time.Second)

	probe := &readPromotionProbe{}
	handler := mw(probe)

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.AddCookie(&http.Cookie{Name: stdcrpcwritefence.DefaultCookieName, Value: value})

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.False(t, probe.got.Load(), "expired cookies MUST NOT promote")
}

func TestMiddleware_marked_intent_sets_cookie(t *testing.T) {
	t.Parallel()

	// A handler that flips the fence-intent flag MUST cause the
	// middleware to add a Set-Cookie header for the configured
	// cookie. This is the round-trip the whole feature depends on.
	handler := markingHandler(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	mw := promotedMiddleware()(handler)

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/w", nil))

	require.Equal(t, http.StatusNoContent, rr.Result().StatusCode)

	c := findSetCookie(t, rr.Result(), stdcrpcwritefence.DefaultCookieName)
	require.NotNil(t, c, "marked fence intent MUST emit a Set-Cookie")
	assert.NotEmpty(t, c.Value, "cookie value must be non-empty")
	assert.Equal(t, "/", c.Path, "default cookie Path must be /")
	assert.True(t, c.HttpOnly, "cookie must be HttpOnly")
	assert.True(t, c.Secure, "cookie must be Secure by default")
	assert.Equal(t, http.SameSiteLaxMode, c.SameSite, "default SameSite must be Lax")
	assert.Equal(t, int(stdcrpcwritefence.DefaultTTL.Seconds()), c.MaxAge,
		"cookie MaxAge must equal the configured TTL in seconds")
}

func TestMiddleware_no_marked_intent_no_cookie(t *testing.T) {
	t.Parallel()

	// A handler that does NOT flip the fence-intent flag MUST NOT
	// cause a Set-Cookie. The fence-intent flag is the sole trigger
	// — there is no implicit "POST always pins" rule, by design.
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := promotedMiddleware()(handler)

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/r", nil))

	require.Nil(t, findSetCookie(t, rr.Result(), stdcrpcwritefence.DefaultCookieName),
		"un-flipped fence intent MUST NOT pin the cookie")
}

func TestMiddleware_handler_returns_without_writing(t *testing.T) {
	t.Parallel()

	// A handler that flips the fence-intent flag but never calls
	// WriteHeader / Write must still get the cookie set — the
	// middleware covers this case by re-running setCookie after
	// next.ServeHTTP.
	handler := markingHandler(nil)

	mw := promotedMiddleware()(handler)

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/w", nil))

	require.NotNil(t, findSetCookie(t, rr.Result(), stdcrpcwritefence.DefaultCookieName),
		"marked fence intent must pin cookie even when handler returns without writing")
}

func TestMiddleware_cookie_set_before_writeheader_flush(t *testing.T) {
	t.Parallel()

	// The cookie must be present on the response headers OBSERVED by
	// the client — i.e. it must be added before WriteHeader flushes.
	// Calling WriteHeader explicitly inside the handler is the
	// scenario most likely to expose an ordering bug.
	handler := markingHandler(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	})

	mw := promotedMiddleware()(handler)

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/w", nil))

	res := rr.Result()
	require.Equal(t, http.StatusCreated, res.StatusCode)
	require.NotNil(t, findSetCookie(t, res, stdcrpcwritefence.DefaultCookieName),
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

	handler := markingHandler(nil)

	mw := promotedMiddleware(
		stdcrpcwritefence.WithCookieName(name),
		stdcrpcwritefence.WithPath(path),
		stdcrpcwritefence.WithDomain(domain),
		stdcrpcwritefence.WithSameSite(http.SameSiteStrictMode),
		stdcrpcwritefence.WithInsecure(),
		stdcrpcwritefence.WithTTL(ttl),
	)(handler)

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/w", nil))

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
	writerHandler := markingHandler(nil)

	// Issue under the default name.
	defaultMW := promotedMiddleware()(writerHandler)

	wrr := httptest.NewRecorder()
	defaultMW.ServeHTTP(wrr, httptest.NewRequest(http.MethodPost, "/w", nil))

	issued := findSetCookie(t, wrr.Result(), stdcrpcwritefence.DefaultCookieName)
	require.NotNil(t, issued)

	// Replay against a middleware configured to look for a DIFFERENT
	// cookie name — it must not promote.
	probe := &readPromotionProbe{}
	customMW := promotedMiddleware(stdcrpcwritefence.WithCookieName("other"))(probe)

	req := httptest.NewRequest(http.MethodGet, "/r", nil)
	req.AddCookie(issued)

	rrr := httptest.NewRecorder()
	customMW.ServeHTTP(rrr, req)

	assert.False(t, probe.got.Load(),
		"a middleware looking for a different cookie name MUST ignore the default-named cookie")
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

	_ = stdcrpcwritefence.Middleware([]byte("too-short"))
}

func TestMiddleware_does_not_write_on_read_path(t *testing.T) {
	t.Parallel()

	// On any read path (no marked intent), the middleware MUST NOT
	// touch the response — not even to clear an existing cookie. The
	// browser is the authority on cookie lifetime; the server only
	// pins, never unpins.
	probe := &readPromotionProbe{}
	mw := promotedMiddleware()(probe)

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/x", nil))

	res := rr.Result()
	assert.Empty(t, res.Cookies(), "no marked intent means no Set-Cookie")
	// httptest.NewRecorder defaults to 200, but only because that's
	// the recorder's zero value. We assert nothing else was set.
	assert.Empty(t, res.Header.Get("Set-Cookie"),
		"middleware must not write any Set-Cookie on a pure read path")
}

func TestMarkFenceIntent_outside_middleware_is_noop(t *testing.T) {
	t.Parallel()

	// MarkFenceIntent on a ctx that never went through Middleware
	// must be a silent no-op — non-HTTP consumers (Temporal
	// activities, CLI bootstrap, tests) are unaffected.
	require.NotPanics(t, func() {
		stdcrpcwritefence.MarkFenceIntent(t.Context())
	})
}
