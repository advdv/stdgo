package stdbifrostfx_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/advdv/stdgo/fx/stdbifrostfx"
	"github.com/advdv/stdgo/fx/stdhttpclientfx"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/cockroachdb/errors"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

// setup boots the module on a gateway that answers every request with
// status and body, and returns the client and the requests the gateway got.
func setup(tb testing.TB, status int, body string) (*stdbifrostfx.Client, *[]map[string]any) {
	tb.Helper()

	var got []map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if r.URL.Path != "/v1/chat/completions" || json.NewDecoder(r.Body).Decode(&req) != nil {
			w.WriteHeader(http.StatusNotFound)

			return
		}

		got = append(got, req)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	tb.Cleanup(srv.Close)

	var client *stdbifrostfx.Client

	app := fxtest.New(tb,
		stdenvcfg.ProvideExplicitEnvironment(map[string]string{
			"STDBIFROST_URL":   srv.URL,
			"STDBIFROST_MODEL": "provider/model",
		}),
		stdzapfx.Fx(),
		stdzapfx.TestProvide(tb),
		stdhttpclientfx.Provide(),
		stdbifrostfx.Provide(),
		fx.Populate(&client),
	)
	app.RequireStart()
	tb.Cleanup(app.RequireStop)

	return client, &got
}

func TestFirstTokenNormalizesAndAddsUpCandidates(t *testing.T) {
	t.Parallel()

	// ln(0.5), ln(0.25), ln(0.125)
	client, got := setup(t, http.StatusOK, `{"choices":[{"logprobs":{"content":[{
		"token":"Yes","logprob":-0.6931471805599453,
		"top_logprobs":[
			{"token":"Yes","logprob":-0.6931471805599453},
			{"token":" yes","logprob":-1.3862943611198906},
			{"token":"No","logprob":-2.0794415416798357}]}]}}]}`)

	tokens, err := client.FirstToken(t.Context(), "Is it?")
	require.NoError(t, err)
	require.InDelta(t, 0.75, tokens["yes"], 1e-9)
	require.InDelta(t, 0.125, tokens["no"], 1e-9)
	require.Len(t, tokens, 2)

	require.Len(t, *got, 1)
	req := (*got)[0]
	require.Equal(t, "provider/model", req["model"])
	require.Equal(t, true, req["logprobs"])
	require.InDelta(t, stdbifrostfx.MaxCandidates, req["top_logprobs"], 0)
	require.InDelta(t, 1, req["max_tokens"], 0)
	require.Equal(t, "none", req["reasoning_effort"])
}

func TestFirstTokenWithoutTopLogprobsUsesTheToken(t *testing.T) {
	t.Parallel()

	client, _ := setup(t, http.StatusOK, `{"choices":[{"logprobs":{"content":[{"token":"No","logprob":0}]}}]}`)

	tokens, err := client.FirstToken(t.Context(), "Is it?")
	require.NoError(t, err)
	require.Equal(t, map[string]float64{"no": 1}, tokens)
}

func TestFirstTokenWithoutLogprobsFails(t *testing.T) {
	t.Parallel()

	client, _ := setup(t, http.StatusOK, `{"choices":[{"message":{"content":"yes"}}]}`)

	_, err := client.FirstToken(t.Context(), "Is it?")
	require.ErrorIs(t, err, stdbifrostfx.ErrNoLogprobs)
}

func TestFirstTokenIsRejectedOnClientErrorsButNotOnRateLimits(t *testing.T) {
	t.Parallel()

	for status, rejected := range map[int]bool{
		http.StatusUnprocessableEntity: true,
		http.StatusForbidden:           true,
		http.StatusTooManyRequests:     false,
		http.StatusBadGateway:          false,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()

			client, _ := setup(t, status, `{"error":{"message":"the prompt, quoted"}}`)

			_, err := client.FirstToken(t.Context(), "Is it?")
			require.Error(t, err)
			require.Equal(t, rejected, errors.Is(err, stdbifrostfx.ErrRejected), err)
			require.NotContains(t, err.Error(), "the prompt, quoted")
		})
	}
}

func TestFirstTokenSendsImagesAfterThePrompt(t *testing.T) {
	t.Parallel()

	client, got := setup(t, http.StatusOK, `{"choices":[{"logprobs":{"content":[{"token":"yes","logprob":0}]}}]}`)

	_, err := client.FirstToken(t.Context(), "Is this a certificate?",
		stdbifrostfx.Image{MediaType: "image/png", Data: []byte("\x89PNG")})
	require.NoError(t, err)
	require.Len(t, *got, 1)
	require.Equal(t, []any{map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "text", "text": "Is this a certificate?"},
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,iVBORw=="}},
	}}}, (*got)[0]["messages"])
}
