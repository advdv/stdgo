// Package stdbifrosttestfx provides the stdbifrostfx client for tests, which replays
// the gateway's answers from recordings, in testdata/bifrost/<test name>.yaml of
// the test's package, so tests need no gateway, provider, or key. A request
// that has no recording fails the test.
//
// With STDBIFROSTTEST_RECORD=1 the tests record instead: they call a Bifrost
// gateway that is started for them ([stdtestcontainer.Bifrost]), which calls
// the real providers, and overwrite their recordings. Only test code should
// import this package: it pulls in testcontainers-go and the Docker client.
package stdbifrosttestfx

import (
	"bytes"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/advdv/stdgo/fx/stdbifrostfx"
	"github.com/advdv/stdgo/fx/stdhttpclientfx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/advdv/stdgo/stdtestcontainer"
	"github.com/cockroachdb/errors"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/recorder"
)

// replayHost is where the client sends its requests while replaying: nothing
// answers there, and recordings name it instead of the gateway they had.
const replayHost = "stdbifrosttest.invalid"

// Gateway describes the Bifrost gateway that the tests record against.
type Gateway struct {
	// Image is the gateway's image, such as the one Docker Compose runs.
	Image string
	// Config is the path of the gateway's config.json.
	Config string
	// Env names the environment variables that the gateway gets from the
	// tests' own environment, such as its providers' keys, which Config
	// references as env.<NAME>. Recording fails when one is unset.
	Env []string
}

// Provide works like [stdbifrostfx.Provide], but replays the recordings of tb, or
// records them against gateway with STDBIFROSTTEST_RECORD=1. The test's
// environment still sets STDBIFROST_MODEL. It needs stdhttpclientfx's client, and
// passes the client's other requests on.
func Provide(tb testing.TB, gateway Gateway) fx.Option {
	tb.Helper()

	record, _ := strconv.ParseBool(os.Getenv("STDBIFROSTTEST_RECORD"))

	endpoint := "http://" + replayHost
	if record {
		endpoint = start(tb, gateway)
	}

	return fx.Module("stdbifrosttest",
		// Scoped to this module, so only the client sees the gateway.
		fx.Decorate(func(env stdenvcfg.Environment) stdenvcfg.Environment {
			out := maps.Clone(env)
			out["STDBIFROST_URL"] = endpoint

			return out
		}),
		stdbifrostfx.Provide(),
		fx.Supply(fx.Annotate(recording(tb, endpoint, record), fx.ResultTags(`group:"stdhttpclient.middleware"`))),
	)
}

// start returns the URL of the gateway, which the first test that records
// starts.
func start(tb testing.TB, gateway Gateway) string {
	tb.Helper()

	env := make(map[string]string, len(gateway.Env))
	for _, name := range gateway.Env {
		env[name] = os.Getenv(name)
		require.NotEmpty(tb, env[name], "recording needs %s in the environment", name)
	}

	return stdtestcontainer.Bifrost(tb, gateway.Image, gateway.Config, env).Endpoint
}

// recording returns a middleware that replays the requests to gateway from
// tb's recording, or with record records them, and passes on the others. It
// fails tb when tb has no recording to replay.
func recording(tb testing.TB, gateway string, record bool) stdhttpclientfx.Middleware {
	tb.Helper()

	parsed, err := url.Parse(gateway)
	require.NoError(tb, err, "parse gateway url")

	mode := recorder.ModeReplayOnly
	if record {
		mode = recorder.ModeRecordOnly
	}

	name := filepath.Join("testdata", "bifrost", tb.Name())

	rec, err := recorder.New(name,
		recorder.WithMode(mode),
		recorder.WithSkipRequestLatency(true),
		recorder.WithReplayableInteractions(true),
		recorder.WithPassthrough(func(req *http.Request) bool { return req.URL.Host != parsed.Host }),
		recorder.WithMatcher(matches),
		recorder.WithHook(anonymize, recorder.BeforeSaveHook),
	)
	if errors.Is(err, cassette.ErrCassetteNotFound) {
		require.FailNow(tb, "stdbifrosttest: no recording "+name+".yaml; record it with STDBIFROSTTEST_RECORD=1")
	}

	require.NoError(tb, err, "load recording")
	tb.Cleanup(func() { require.NoError(tb, rec.Stop(), "save recording") })

	return stdhttpclientfx.Middleware{Name: "stdbifrosttest", Wrap: func(next http.RoundTripper) http.RoundTripper {
		recorder.WithRealTransport(next)(rec)

		return replaying{rec: rec, tb: tb}
	}}
}

// replaying fails the test on a request that has no recording, also when the
// code under test handles the error, such as a job that retries.
type replaying struct {
	rec *recorder.Recorder
	tb  testing.TB
}

func (r replaying) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := r.rec.RoundTrip(req)
	if errors.Is(err, cassette.ErrInteractionNotFound) {
		r.tb.Errorf("stdbifrosttest: no recording of %s %s in testdata/bifrost/%s.yaml; "+
			"record it with STDBIFROSTTEST_RECORD=1", req.Method, req.URL.Path, r.tb.Name())
	}

	return resp, err //nolint:wrapcheck // the recorder's or transport's own error
}

// matches reports whether req is the recorded request: the same method, path,
// and body, wherever the gateway was.
func matches(req *http.Request, recorded cassette.Request) bool {
	want, err := url.Parse(recorded.URL)
	if err != nil || req.Method != recorded.Method || req.URL.Path != want.Path {
		return false
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		return false
	}

	req.Body = io.NopCloser(bytes.NewReader(body))

	return string(body) == recorded.Body
}

// anonymize makes a recording the same on every run: it names replayHost
// instead of the gateway, whose port differs, and leaves out the request's
// headers, which matching ignores, and the response's, but its content type.
func anonymize(i *cassette.Interaction) error {
	recorded, err := url.Parse(i.Request.URL)
	if err != nil {
		return errors.Wrap(err, "parse recorded url")
	}

	recorded.Host = replayHost
	i.Request.URL = recorded.String()
	i.Request.Host = replayHost
	i.Request.Headers = nil
	i.Response.Headers = http.Header{"Content-Type": i.Response.Headers.Values("Content-Type")}

	return nil
}
