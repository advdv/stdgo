// Package stdentwritefence provides an HTTP middleware that gives a
// client read-your-writes consistency against an ro/rw transactor
// pair without any server-side state.
//
// It glues two seams that already exist in [stdent]:
//
//   - [stdent.WithReadPromotion] — stamped on a request ctx when the
//     incoming request carries a valid, unexpired write-fence
//     cookie; [stdent.TransactR] / [stdent.TransactR0] then route
//     reads to the rw transactor for the duration of that request.
//
//   - [stdent.WithWriteObserver] — installed on every request ctx
//     before the handler runs; flipped by [stdent.Transact1] on the
//     first successful commit of a non-read-only transactor. The
//     middleware inspects the bit on the way out and pins a fresh
//     cookie on the response when it is set.
//
// The cookie payload is opaque and trivial (a fixed string). All the
// trust lives in the HMAC signature and the [securecookie.MaxAge]
// timestamp embedded by [securecookie]: the signature prevents a
// client from forging or extending the window, the embedded
// timestamp prevents replay past the configured TTL.
//
// The middleware is deliberately independent of any tenancy /
// per-tenant Postgres role concerns — those live in
// fx/stdcrpcenttenancyfx and compose on top of stdent. This package
// only knows about pool routing; it is reusable by any caller that
// has an ro / rw pair behind two [stdent.Transactor] instances.
//
// Failure mode for cookie verification is fail-open: any error
// (cookie missing, malformed, tampered, expired) is treated as "no
// promotion". The middleware never short-circuits the request and
// never writes to the response except to add the Set-Cookie header
// when a write was observed.
package stdentwritefence

import (
	"net/http"
	"sync"
	"time"

	"github.com/advdv/stdgo/stdent"
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

type config struct {
	cookieName string
	path       string
	domain     string
	sameSite   http.SameSite
	secure     bool
	httpOnly   bool
	ttl        time.Duration
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

// Middleware builds an HTTP middleware that implements
// cookie-based read-your-writes routing on top of [stdent]'s
// read-promotion and write-observer seams.
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
//     unexpired cookie it calls [stdent.WithReadPromotion] on
//     ctx so a subsequent [stdent.TransactR] / [stdent.TransactR0]
//     opens against the rw transactor instead of the ro one.
//
//  2. Installs a fresh [stdent.WithWriteObserver] on ctx so any
//     successful non-read-only commit during the handler is
//     observable.
//
//  3. Wraps the [http.ResponseWriter] so that the first call to
//     WriteHeader or Write (whichever lands first) — or, failing
//     either, the moment the handler returns — checks the observer
//     and, if a write was observed, adds a freshly-signed cookie
//     to the response headers BEFORE the status line is flushed to
//     the wire.
func Middleware(hashKey []byte, opts ...Option) func(http.Handler) http.Handler {
	if len(hashKey) < MinHashKeyLen {
		panic("stdentwritefence: hashKey must be at least 32 bytes")
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

			// Read side — verify the cookie and, on success, stamp
			// the promotion bit on ctx. Any failure (missing,
			// malformed, signature mismatch, expired) is silently
			// treated as "no promotion": the middleware never writes
			// to the response on the read path.
			if c, err := r.Cookie(cfg.cookieName); err == nil {
				var v string
				if err := codec.Decode(cfg.cookieName, c.Value, &v); err == nil {
					ctx = stdent.WithReadPromotion(ctx)
				}
			}

			// Write side — install the observer that stdent.Transact1
			// will flip on a successful non-read-only commit.
			ctx, obs := stdent.WithWriteObserver(ctx)

			var once sync.Once

			setCookie := func() {
				once.Do(func() {
					if !obs.Load() {
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
