// Package stdhttpware provides cross-cutting middleware http handlers.
package stdhttpware

import (
	"fmt"
	"net/http"
	"net/netip"

	"github.com/advdv/stdgo/stdctx"
	"github.com/felixge/httpsnoop"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"
)

// Apply applies the middleware in the correct order. The values of sensitiveHeaders, which carry
// credentials, are redacted wherever headers are logged; their case does not matter.
func Apply(mux http.Handler, logs *zap.Logger, sensitiveHeaders []string) http.Handler {
	redact := newRedactor(sensitiveHeaders)
	/* ^ */ mux = loggerMiddleware(logs, redact)(mux)
	/* | */ mux = cacheMiddleware()(mux)
	/* | */ mux = chimiddleware.RealIP(mux)
	/* | */ mux = chimiddleware.RequestID(mux)
	/* | */ mux = recoverMiddleware(logs, redact)(mux) // outer middleware, recover anything.
	return mux
}

// cacheMiddleware initializes middle for disabling caching by default.
func cacheMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if w.Header().Get("Cache-Control") == "" {
				w.Header().Set("Cache-Control", "private, no-store")
			}

			next.ServeHTTP(w, r)
		})
	}
}

// loggerMiddleware adds a zap logger to the reques context for all request handling code to use.
func loggerMiddleware(logs *zap.Logger, redact redactor) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqID := chimiddleware.GetReqID(r.Context())
			logs := logs.With(
				zap.String("host", r.Host),
				zap.String("method", r.Method),
				zap.String("remote_addr", r.RemoteAddr),
				zap.String("content_type", r.Header.Get("Content-Type")),
				zap.String("request_id", reqID),
				zap.String("request_uri", r.RequestURI))

			// in general we wanna show all requests, but the Docker and ALB health checks are too noisy.
			if isHealthCheck(r) {
				logs = zap.NewNop()
			}

			// we set the id on the response so we can more easily trace it.
			w.Header().Set("Sd-Request-Id", reqID)

			ctx := stdctx.WithLogger(r.Context(), logs)
			m := httpsnoop.CaptureMetrics(next, w, r.WithContext(ctx)) // call other middleware.

			logs.Info("request",
				zap.Any("request_header", redact.header(r.Header)),
				zap.Any("response_header", redact.header(w.Header())),
				zap.Int("status", m.Code),
				zap.Int64("bytes_written", m.Written),
				zap.Duration("duration", m.Duration))
		})
	}
}

// redactor redacts the values of sensitive headers, keyed by their canonical form.
type redactor map[string]bool

// newRedactor returns the redactor of the headers named.
func newRedactor(sensitiveHeaders []string) redactor {
	redact := make(redactor, len(sensitiveHeaders))
	for _, key := range sensitiveHeaders {
		redact[http.CanonicalHeaderKey(key)] = true
	}

	return redact
}

// header returns a copy of h with the values of the sensitive headers replaced, so it can be logged.
func (redact redactor) header(h http.Header) http.Header {
	redacted := h.Clone()
	for key, values := range redacted {
		if !redact[http.CanonicalHeaderKey(key)] {
			continue
		}

		for i := range values {
			values[i] = "[REDACTED]"
		}
	}

	return redacted
}

// isHealthCheck reports whether r is a health check whose request log is only noise: one from the ALB
// on a private address, or from wget, which Docker health checks use, on the loopback address.
func isHealthCheck(r *http.Request) bool {
	addr, ok := remoteAddr(r)
	if !ok {
		return false
	}

	switch r.UserAgent() {
	case "ELB-HealthChecker/2.0":
		return addr.IsPrivate()
	case "Wget":
		return addr.IsLoopback()
	default:
		return false
	}
}

// remoteAddr parses the remote address of r. It is an address with a port, as net/http sets it, or a
// bare address, as [chimiddleware.RealIP] sets it.
func remoteAddr(r *http.Request) (netip.Addr, bool) {
	if addrPort, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
		return addrPort.Addr().Unmap(), true
	}

	if addr, err := netip.ParseAddr(r.RemoteAddr); err == nil {
		return addr.Unmap(), true
	}

	return netip.Addr{}, false
}

// recoverMiddleware initializes middleware to recover from panics and log them.
func recoverMiddleware(logs *zap.Logger, redact redactor) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rvr := recover(); rvr != nil {
					if rvr == http.ErrAbortHandler { //nolint:errorlint,err113
						// we don't recover http.ErrAbortHandler so the response
						// to the client is aborted, this should not be logged
						panic(rvr)
					}

					if r.Header.Get("Connection") != "Upgrade" {
						w.WriteHeader(http.StatusInternalServerError)
					}

					var err error
					if rerr, ok := rvr.(error); ok {
						err = rerr
					} else {
						err = fmt.Errorf("non-error panic: %v", rvr) //nolint:err113
					}

					// NOTE: don't log using the contextual logger (with request information) because this recover
					// middleware is the outer middleware so the logger is not in the context yet.
					logs.Error("panic while serving request",
						zap.Stack("stack"),
						zap.Error(err), zap.Any("request_header", redact.header(r.Header)))
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}
