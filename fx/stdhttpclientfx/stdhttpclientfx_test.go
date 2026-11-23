package stdhttpclientfx_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/advdv/stdgo/fx/stdhttpclientfx"
	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/carlmjohnson/requests"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// trace returns a middleware named name that records it saw a request, and
// then its response, in seen.
func trace(name string, seen *[]string) func() stdhttpclientfx.Middleware {
	return func() stdhttpclientfx.Middleware {
		return stdhttpclientfx.Middleware{Name: name, Wrap: func(next http.RoundTripper) http.RoundTripper {
			return roundTripper(func(req *http.Request) (*http.Response, error) {
				*seen = append(*seen, name+" request")
				resp, err := next.RoundTrip(req)
				*seen = append(*seen, name+" response")

				return resp, err
			})
		}}
	}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// setup boots the module with the given options, logging to the returned
// observer.
func setup(tb testing.TB, opts ...fx.Option) (*http.Client, *observer.ObservedLogs) {
	tb.Helper()

	core, logs := observer.New(zap.DebugLevel)

	var client *http.Client

	app := fxtest.New(tb, append(opts,
		stdenvcfg.ProvideExplicitEnvironment(map[string]string{}),
		fx.Supply(zap.New(core)),
		stdhttpclientfx.Provide(),
		fx.Populate(&client),
	)...)
	app.RequireStart()
	tb.Cleanup(app.RequireStop)

	return client, logs
}

func TestMiddlewareRunsInOrderOfName(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(srv.Close)

	var seen []string

	// Provided out of order.
	client, _ := setup(t,
		fx.Provide(stdhttpclientfx.AsMiddleware(trace("b", &seen))),
		fx.Provide(stdhttpclientfx.AsMiddleware(trace("a", &seen))),
	)

	require.NoError(t, requests.URL(srv.URL).Client(client).Fetch(t.Context()))
	require.Equal(t, []string{"a request", "b request", "b response", "a response"}, seen)
}

func TestLogsWithoutPathOrQuery(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(srv.Close)

	client, logs := setup(t)

	err := requests.URL(srv.URL).Path("/bucket/secret-key").Param("X-Amz-Signature", "abc123").
		Client(client).Fetch(t.Context())
	require.Error(t, err)

	host, _ := url.Parse(srv.URL)

	require.Len(t, logs.All(), 1)
	entry := logs.All()[0]
	require.Equal(t, "stdhttpclient", entry.LoggerName)
	require.Equal(t, map[string]any{
		"method":   "GET",
		"host":     host.Host,
		"status":   int64(http.StatusTeapot),
		"duration": entry.ContextMap()["duration"],
	}, entry.ContextMap())
}

func TestLogsWithTheLoggerOfTheContext(t *testing.T) {
	t.Parallel()

	client, moduleLogs := setup(t)

	core, contextLogs := observer.New(zap.DebugLevel)
	ctx := stdctx.WithLogger(t.Context(), zap.New(core))

	// Nothing listens on port 1, so the request fails.
	err := requests.URL("http://127.0.0.1:1").Client(client).Fetch(ctx)
	require.Error(t, err)

	require.Zero(t, moduleLogs.Len())
	require.Len(t, contextLogs.FilterMessage("http request failed").All(), 1)
}
