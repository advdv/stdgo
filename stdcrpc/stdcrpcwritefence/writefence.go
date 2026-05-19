// Package stdcrpcwritefence gives a client read-your-writes consistency
// against an ro/rw transactor pair without any server-side state.
//
// It is composed of two pieces that share a single, package-private
// ctx flag:
//
//   - [Middleware] — an HTTP middleware that
//     (a) verifies the inbound write-fence cookie and, on success,
//     hands the request ctx to a caller-supplied promoter
//     ([WithReadPromotion]) — typically `stdent.WithReadPromotion` —
//     so a subsequent transact call can route the read to the rw
//     transactor, and
//     (b) installs a fresh fence-intent flag on the request ctx;
//     on the way out, if anything flipped that flag, the middleware
//     pins a freshly-signed cookie on the response BEFORE the status
//     line is flushed.
//
//   - [Interceptor] — a server-side ConnectRPC unary interceptor
//     that flips the fence-intent flag whenever the inbound
//     procedure's idempotency level is anything other than
//     NO_SIDE_EFFECTS and the handler returned nil. The decision is
//     read straight off [connect.Spec.IdempotencyLevel], which is in
//     turn driven by the procedure's `idempotency_level` proto
//     annotation — no codegen, no bespoke annotation, and no
//     handler-body changes required.
//
// The package is intentionally not tied to ent: the write-detection
// side is purely wire-level (the interceptor) and the read-side
// ctx stamp is delegated to a caller-supplied hook via
// [WithReadPromotion]. The composition root supplies the concrete
// promoter (typically `stdent.WithReadPromotion`) at wiring time —
// keeping this package free of any ent / transactor dependency.
//
// The cookie payload is opaque and trivial (a fixed string). All the
// trust lives in the HMAC signature and the [securecookie.MaxAge]
// timestamp embedded by [securecookie]: the signature prevents a
// client from forging or extending the window, the embedded
// timestamp prevents replay past the configured TTL.
//
// Failure mode for cookie verification is fail-open: any error
// (cookie missing, malformed, tampered, expired) is treated as "no
// promotion". The middleware never short-circuits the request and
// never writes to the response except to add the Set-Cookie header
// when a write was observed.
package stdcrpcwritefence

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/felixge/httpsnoop"
	"github.com/gorilla/securecookie"
)

const (
	// DefaultCookieName is the cookie name used when [WithCookieName]
	// is not supplied. The leading underscore mirrors the "host-only,
	// internal" convention some browsers / proxies treat specially.
	DefaultCookieName = "_wfence"

	// DefaultTTL is the read-your-writes window applied to every
	// pinned response when [WithTTL] is not supplied. Three seconds
	// is a typical Aurora / RDS reader-lag budget; tune per
	// deployment.
	DefaultTTL = 3 * time.Second

	// MinHashKeyLen is the minimum hash key length enforced at
	// construction time. 32 bytes is the size of an HMAC-SHA-256
	// key — anything shorter weakens the signature for no benefit.
	MinHashKeyLen = 32

	// cookieValue is the fixed payload encoded into the cookie. The
	// trust is in the signature + securecookie's embedded timestamp,
	// not in the payload itself, so the value is arbitrary.
	cookieValue = "1"
)

// fenceIntentKey is the unexported context key stamped on every
// request ctx by [Middleware] and flipped by [Interceptor] (or by
// any caller that explicitly invokes [MarkFenceIntent]). Kept
// unexported on purpose: the only legitimate flippers are the
// interceptor and [MarkFenceIntent], the only legitimate reader is
// the middleware. Both live in this package — no opaque cross-package
// surface.
type fenceIntentKey struct{}

// withFenceIntent attaches a fresh fence-intent flag to ctx and
// returns it. Middleware-internal: the returned pointer is the one
// the middleware inspects on the way out. Callers that want to flip
// the flag go through [MarkFenceIntent].
func withFenceIntent(ctx context.Context) (context.Context, *atomic.Bool) {
	b := &atomic.Bool{}

	return context.WithValue(ctx, fenceIntentKey{}, b), b
}

// MarkFenceIntent flips the fence-intent flag attached to ctx by
// [Middleware], if any. The [Interceptor] is the canonical flipper
// and covers every Connect handler whose procedure is not annotated
// `idempotency_level = NO_SIDE_EFFECTS`. This helper exists for the
// rare case where a non-Connect code path knows it wrote and wants
// the same fence semantics — e.g. a hand-rolled HTTP handler that
// mutates state outside the Connect chain.
//
// A no-op (one ctx lookup, type assertion fails) when no fence
// intent is attached, so callers outside an HTTP request scope (CLI
// bootstrap, Temporal activities, tests) are unaffected.
//
// Symmetry with [stdent.WithReadPromotion]: the read side exports a
// public ctx stamper (stamp from anywhere, consumed by the routing
// helpers), the write side exports a public marker (mark from
// anywhere, consumed by this package's middleware).
func MarkFenceIntent(ctx context.Context) {
	if b, ok := ctx.Value(fenceIntentKey{}).(*atomic.Bool); ok {
		b.Store(true)
	}
}

type config struct {
	cookieName string
	path       string
	domain     string
	sameSite   http.SameSite
	secure     bool
	httpOnly   bool
	ttl        time.Duration
	promote    func(context.Context) context.Context
}

// Option configures a [Middleware].
type Option func(*config)

// WithCookieName overrides the cookie name used by the middleware.
// Use this to namespace the cookie per-app when multiple apps
// share the same domain.
func WithCookieName(name string) Option { return func(c *config) { c.cookieName = name } }

// WithPath overrides the cookie's Path attribute. Defaults to "/".
func WithPath(p string) Option { return func(c *config) { c.path = p } }

// WithDomain sets the cookie's Domain attribute. Empty (the
// default) means host-only.
func WithDomain(d string) Option { return func(c *config) { c.domain = d } }

// WithSameSite overrides the cookie's SameSite attribute. Defaults
// to [http.SameSiteLaxMode] which is correct for typical web apps;
// switch to [http.SameSiteStrictMode] for stricter origin pinning.
func WithSameSite(s http.SameSite) Option { return func(c *config) { c.sameSite = s } }

// WithInsecure drops the Secure attribute from the issued cookie.
// Use only for local development over plain HTTP; production
// deployments MUST keep Secure on (the default).
func WithInsecure() Option { return func(c *config) { c.secure = false } }

// WithTTL overrides the read-your-writes window. The same duration
// is set both on the issued cookie's Max-Age (browser-side expiry)
// and on the underlying [securecookie] codec (server-side
// validation), so a client cannot extend the window by replaying an
// old cookie.
func WithTTL(d time.Duration) Option { return func(c *config) { c.ttl = d } }

// WithReadPromotion sets the ctx promoter the middleware invokes on
// a successful cookie verification. The promoter returns a derived
// ctx that downstream code (typically `stdent.TransactR` /
// `stdent.TransactR0`) consults to route the read to the rw
// transactor.
//
// Composition roots wire this with `stdent.WithReadPromotion` (or
// any equivalent ctx stamp from a different routing layer). Leaving
// it unset means the middleware verifies the cookie but does not
// modify ctx — useful when the cookie is only there to drive
// fence-intent reasoning and routing is handled elsewhere.
//
// Keeping the promoter behind an option is the single seam by which
// this package avoids a hard dependency on stdent: the read-side
// stamp is injected by the caller, the write-side flag is owned
// here.
func WithReadPromotion(promote func(context.Context) context.Context) Option {
	return func(c *config) { c.promote = promote }
}

// Middleware builds an HTTP middleware that implements
// cookie-based read-your-writes routing on top of this package's
// fence-intent flag and a caller-supplied ctx promoter (see
// [WithReadPromotion]).
//
// hashKey is the HMAC-SHA-256 key used to sign / verify cookies.
// It MUST be at least [MinHashKeyLen] bytes; shorter keys panic at
// construction time (programmer error). The key is the only piece
// of secret material in the middleware — rotating it soft-
// invalidates every in-flight cookie, which is the desired
// behaviour during key rotation.
//
// The returned middleware:
//
//  1. Reads the configured cookie from the request; on a verified,
//     unexpired cookie it hands ctx to the promoter supplied via
//     [WithReadPromotion] (if any) so a subsequent transact call
//     can open against the rw transactor instead of the ro one.
//
//  2. Installs a fresh fence-intent flag on ctx so any caller down
//     the stack (canonically [Interceptor], optionally
//     [MarkFenceIntent]) can request a cookie pin.
//
//  3. Wraps the [http.ResponseWriter] so that the first call to
//     WriteHeader or Write (whichever lands first) — or, failing
//     either, the moment the handler returns — checks the flag
//     and, if set, adds a freshly-signed cookie to the response
//     headers BEFORE the status line is flushed to the wire.
func Middleware(hashKey []byte, opts ...Option) func(http.Handler) http.Handler {
	if len(hashKey) < MinHashKeyLen {
		panic("stdcrpcwritefence: hashKey must be at least 32 bytes")
	}

	cfg := config{
		cookieName: DefaultCookieName,
		path:       "/",
		sameSite:   http.SameSiteLaxMode,
		secure:     true,
		httpOnly:   true,
		ttl:        DefaultTTL,
	}

	for _, o := range opts {
		o(&cfg)
	}

	codec := securecookie.New(hashKey, nil)
	// MaxAge is in whole seconds in securecookie; ensure at least 1
	// so a sub-second TTL still enables the timestamp check.
	maxAgeSeconds := max(int(cfg.ttl.Seconds()), 1)

	codec.MaxAge(maxAgeSeconds)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			// Read side — verify the cookie and, on success, hand
			// ctx to the caller-supplied promoter (if any). Any
			// failure (missing, malformed, signature mismatch,
			// expired) is silently treated as "no promotion": the
			// middleware never writes to the response on the read
			// path.
			if c, err := r.Cookie(cfg.cookieName); err == nil {
				var v string
				if err := codec.Decode(cfg.cookieName, c.Value, &v); err == nil && cfg.promote != nil {
					ctx = cfg.promote(ctx)
				}
			}

			// Write side — install the fence-intent flag that
			// [Interceptor] (or [MarkFenceIntent]) will flip when
			// the procedure's idempotency level demands a fence.
			ctx, intent := withFenceIntent(ctx)

			var once sync.Once

			setCookie := func() {
				once.Do(func() {
					if !intent.Load() {
						return
					}

					encoded, err := codec.Encode(cfg.cookieName, cookieValue)
					if err != nil {
						return
					}

					// gosec G124 flags cookies whose Secure attribute may be
					// false. WithInsecure() is an explicit opt-in for local
					// dev only — production callers leave Secure on (the
					// default).
					http.SetCookie(w, &http.Cookie{ //nolint:exhaustruct,gosec
						Name:     cfg.cookieName,
						Value:    encoded,
						Path:     cfg.path,
						Domain:   cfg.domain,
						MaxAge:   maxAgeSeconds,
						HttpOnly: cfg.httpOnly,
						Secure:   cfg.secure,
						SameSite: cfg.sameSite,
					})
				})
			}

			// Hook WriteHeader and Write so the cookie lands BEFORE
			// the status line / body is flushed. httpsnoop preserves
			// the Flusher / Hijacker / Pusher interfaces so chi /
			// WebSocket upgrades / SSE handlers continue to work.
			wrapped := httpsnoop.Wrap(w, httpsnoop.Hooks{
				WriteHeader: func(next httpsnoop.WriteHeaderFunc) httpsnoop.WriteHeaderFunc {
					return func(code int) {
						setCookie()
						next(code)
					}
				},
				Write: func(next httpsnoop.WriteFunc) httpsnoop.WriteFunc {
					return func(p []byte) (int, error) {
						setCookie()

						return next(p)
					}
				},
			})

			next.ServeHTTP(wrapped, r.WithContext(ctx))

			// Cover the "handler returned without writing" path:
			// net/http's implicit 200 OK is emitted by the server
			// after the handler returns, but the headers it flushes
			// at that point still come from w.Header(). Calling
			// setCookie here is a no-op if the WriteHeader/Write hook
			// already ran, so it's safe in all cases.
			setCookie()
		})
	}
}
