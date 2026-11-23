// Package stdhttpclientfx provides the one HTTP client an application sends
// its outgoing requests with, so behaviour that every request needs, such as
// logging, is added once, as middleware, instead of in each client.
//
// The client logs every request it sends, and runs the middleware that other
// modules provide with [AsMiddleware]. It sets no overall timeout: callers
// bound each request with its context. It does not retry.
package stdhttpclientfx

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdfx"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// Config configures the module, from the environment with the
// STDHTTPCLIENT_ prefix.
type Config struct {
	// MaxIdleConnsPerHost is how many idle connections the client keeps open
	// to each host. Go's default of 2 makes concurrent workers calling one
	// service open and close connections all the time.
	MaxIdleConnsPerHost int `env:"MAX_IDLE_CONNS_PER_HOST" envDefault:"64"`
}

// Middleware wraps the client's transport. Name orders it: the client runs
// middleware in order of name, the first outermost, so it sees a request
// first and its response last.
type Middleware struct {
	Name string
	Wrap func(http.RoundTripper) http.RoundTripper
}

// AsMiddleware annotates a constructor that returns a [Middleware], for
// fx.Provide, so the client runs it.
func AsMiddleware(constructor any) any {
	return fx.Annotate(constructor, fx.ResultTags(`group:"stdhttpclient.middleware"`))
}

// Params is the fx input for [New].
type Params struct {
	fx.In
	fx.Lifecycle

	Config     Config
	Logs       *zap.Logger
	Middleware []Middleware `group:"stdhttpclient.middleware"`
}

// Result is the fx output of [New].
type Result struct {
	fx.Out

	// Client sends requests through Transport.
	Client *http.Client
	// Transport is the client's transport, with the logging and middleware,
	// for libraries that take a transport rather than a client.
	Transport http.RoundTripper
}

// New builds the client.
func New(params Params) (Result, error) {
	base, _ := http.DefaultTransport.(*http.Transport)
	base = base.Clone()
	base.MaxIdleConnsPerHost = params.Config.MaxIdleConnsPerHost

	params.Append(fx.Hook{OnStop: func(context.Context) error {
		base.CloseIdleConnections()

		return nil
	}})

	middleware := slices.SortedFunc(slices.Values(params.Middleware), func(a, b Middleware) int {
		return cmp.Compare(a.Name, b.Name)
	})

	var transport http.RoundTripper = base
	for _, mw := range slices.Backward(middleware) {
		transport = mw.Wrap(transport)
	}

	transport = logging{next: transport, logs: params.Logs}

	return Result{Client: &http.Client{Transport: transport}, Transport: transport}, nil
}

// logging logs every request with the logger of its context, or the
// module's when it has none.
type logging struct {
	next http.RoundTripper
	logs *zap.Logger
}

// RoundTrip logs the request once its response headers arrive, or it fails.
// It logs the method and host, but no path, query, header, or body: those of
// pre-signed requests hold their signature, and a body may be large.
func (l logging) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := l.next.RoundTrip(req)

	logs, ok := stdctx.Logger(req.Context())
	if !ok {
		logs = l.logs
	}

	fields := []zap.Field{
		zap.String("method", req.Method),
		zap.String("host", req.URL.Host),
		zap.Duration("duration", time.Since(start)),
	}

	if err != nil {
		logs.Info("http request failed", append(fields, zap.Error(err))...)

		return resp, err //nolint:wrapcheck // the transport's own error, for the caller to inspect
	}

	logs.Info("http request", append(fields, zap.Int("status", resp.StatusCode))...)

	return resp, nil
}

// Provide wires the module.
func Provide() fx.Option {
	return stdfx.ZapEnvCfgModule[Config]("stdhttpclient", New)
}
