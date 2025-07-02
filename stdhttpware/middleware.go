// Package stdhttpware provides cross-cutting middleware http handlers.
package stdhttpware

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/advdv/stdgo/stdctx"
	"github.com/felixge/httpsnoop"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"
)

// Apply applies the middleware in the correct order.
func Apply(mux http.Handler, logs *zap.Logger) http.Handler {
	/* ^ */ mux = loggerMiddleware(logs)(mux)
	/* | */ mux = cacheMiddleware()(mux)
	/* | */ mux = chimiddleware.RealIP(mux)
	/* | */ mux = chimiddleware.RequestID(mux)
	/* | */ mux = recoverMiddleware(logs)(mux) // outer middleware, recover anything.
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
func loggerMiddleware(logs *zap.Logger) func(http.Handler) http.Handler {
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
			if r.UserAgent() == "ELB-HealthChecker/2.0" && strings.HasPrefix(r.RemoteAddr, "10.0.") ||
				r.UserAgent() == "Wget" && strings.HasPrefix(r.RemoteAddr, "127.0.0.1") {
				logs = zap.NewNop()
			}

			// we set the id on the response so we can more easily trace it.
			w.Header().Set("Sd-Request-Id", reqID)

			ctx := stdctx.WithLogger(r.Context(), logs)
			m := httpsnoop.CaptureMetrics(next, w, r.WithContext(ctx)) // call other middleware.

			logs.Info("request",
				zap.Any("request_header", r.Header),
				zap.Any("response_header", w.Header()),
				zap.Int("status", m.Code),
				zap.Int64("bytes_written", m.Written),
				zap.Duration("duration", m.Duration))
		})
	}
}

// recoverMiddleware initializes middleware to recover from panics and log them.
func recoverMiddleware(logs *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rvr := recover(); rvr != nil {
					if rvr == http.ErrAbortHandler { //nolint:errorlint,goerr113
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
						err = fmt.Errorf("non-error panic: %v", rvr) //nolint:goerr113
					}

					// NOTE: don't log using the contextual logger (with request information) because this recover
					// middleware is the outer middleware so the logger is not in the context yet.
					logs.Error("panic while serving request",
						zap.Stack("stack"),
						zap.Error(err), zap.Any("request_header", r.Header))
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}
