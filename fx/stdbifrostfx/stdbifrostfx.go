// Package stdbifrostfx provides a client of Bifrost (https://docs.getbifrost.ai),
// an LLM gateway, so the service names a model and leaves which provider serves
// it to the deployment's gateway configuration. It speaks Bifrost's
// OpenAI-compatible API, and is the place for Bifrost's own features, such as
// its x-bf-* request headers.
//
// The client asks for log probabilities rather than text: [Client.FirstToken]
// returns how likely the model finds each token it considered as the first of
// its answer, so callers compute decisions from them instead of asking the
// model for a score.
package stdbifrostfx

import (
	"context"
	"encoding/base64"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/advdv/stdgo/stdfx"
	"github.com/carlmjohnson/requests"
	"github.com/cockroachdb/errors"
	"go.uber.org/fx"
)

// ErrRejected reports that Bifrost or the provider behind it refused a
// request, such as for a model it does not serve, or with a key it does not
// accept; sending it again fails the same way.
var ErrRejected = errors.New("stdbifrostfx: rejected")

// ErrNoLogprobs reports that the model answered without log probabilities,
// such as when Bifrost routes it to a provider that has none.
var ErrNoLogprobs = errors.New("stdbifrostfx: no logprobs")

// Config configures the module, from the environment with the STDBIFROST_ prefix.
type Config struct {
	// URL is where the service reaches the gateway.
	URL string `env:"URL,required"`
	// Model is the model the gateway routes questions to, as
	// <provider>/<model> of its configuration.
	Model string `env:"MODEL,required"`
	// Timeout bounds each question, including the gateway's retries.
	Timeout time.Duration `env:"TIMEOUT" envDefault:"1m"`
}

// Client asks the gateway's models questions. It is safe for concurrent use.
type Client struct {
	cfg  Config
	http *http.Client
}

// New builds the client, which sends its requests with client, such as that
// of stdhttpclientfx.
func New(cfg Config, client *http.Client) *Client {
	return &Client{cfg: cfg, http: client}
}

// Model returns the model the client asks.
func (c *Client) Model() string { return c.cfg.Model }

// MaxCandidates is how many tokens [Client.FirstToken] asks for; OpenAI and
// most compatible providers allow up to 20.
const MaxCandidates = 20

// Image is an image a prompt shows the model, such as a PNG, sent inline: a
// link would have to reach the provider, and would let whoever sees the
// request fetch the image.
type Image struct {
	// MediaType is the image's type, such as "image/png".
	MediaType string
	Data      []byte
}

// FirstToken asks the model prompt for a one-token answer, without reasoning
// first, and returns the tokens it considered for it with their
// probabilities. Tokens are trimmed and lower-cased, and the probabilities of
// tokens that are then the same are added up. Images follow the prompt, in
// order, for a model that reads images; a prompt can refer to them by
// position.
//
// It fails with [ErrRejected] when the gateway refuses the request, such as
// with images for a model that reads none, and with [ErrNoLogprobs] when the
// model answers without log probabilities. Its errors never quote the
// response body, which may quote the prompt.
func (c *Client) FirstToken(ctx context.Context, prompt string, images ...Image) (map[string]float64, error) {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	var resp chatResponse

	err := requests.URL(c.cfg.URL).
		Client(c.http).
		Path("/v1/chat/completions").
		BodyJSON(chatRequest{
			Model:           c.cfg.Model,
			Messages:        []message{{Role: "user", Content: content(prompt, images)}},
			MaxTokens:       1,
			Logprobs:        true,
			TopLogprobs:     MaxCandidates,
			ReasoningEffort: "none",
		}).
		ToJSON(&resp).
		Fetch(ctx)

	var response *requests.ResponseError
	if errors.As(err, &response) && response.StatusCode >= 400 && response.StatusCode < 500 &&
		response.StatusCode != http.StatusTooManyRequests {
		return nil, errors.Wrapf(errors.Mark(err, ErrRejected), "chat completion: status %d", response.StatusCode)
	}

	if err != nil {
		return nil, errors.Wrap(err, "chat completion")
	}

	if len(resp.Choices) == 0 || resp.Choices[0].Logprobs == nil || len(resp.Choices[0].Logprobs.Content) == 0 {
		return nil, errors.WithStack(ErrNoLogprobs)
	}

	first := resp.Choices[0].Logprobs.Content[0]

	candidates := first.TopLogprobs
	if len(candidates) == 0 {
		candidates = []candidate{first.candidate}
	}

	out := map[string]float64{}
	for _, cand := range candidates {
		out[strings.ToLower(strings.TrimSpace(cand.Token))] += math.Exp(cand.Logprob)
	}

	return out, nil
}

// message is a message of a chat completion.
type message struct {
	Role string `json:"role"`
	// Content is the text, or the text and images as parts.
	Content any `json:"content"`
}

// part is a part of a message's content: its text, or an image.
type part struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

// imageURL is an image of a part, as a data URL.
type imageURL struct {
	URL string `json:"url"`
}

// content is a message's content: the prompt as plain text, as every
// provider takes it, or the prompt and then the images as parts.
func content(prompt string, images []Image) any {
	if len(images) == 0 {
		return prompt
	}

	parts := make([]part, 0, 1+len(images))
	parts = append(parts, part{Type: "text", Text: prompt})

	for _, img := range images {
		parts = append(parts, part{Type: "image_url", ImageURL: &imageURL{
			URL: "data:" + img.MediaType + ";base64," + base64.StdEncoding.EncodeToString(img.Data),
		}})
	}

	return parts
}

// chatRequest is the body of a chat completion request.
type chatRequest struct {
	Model       string    `json:"model"`
	Messages    []message `json:"messages"`
	MaxTokens   int       `json:"max_tokens"`
	Temperature float64   `json:"temperature"`
	Logprobs    bool      `json:"logprobs"`
	TopLogprobs int       `json:"top_logprobs"`
	// ReasoningEffort "none" keeps reasoning models from reasoning before
	// they answer, which the first token's logprobs would otherwise describe.
	ReasoningEffort string `json:"reasoning_effort"`
}

// candidate is a token the model considered, with its log probability.
type candidate struct {
	Token   string  `json:"token"`
	Logprob float64 `json:"logprob"`
}

// chatResponse is the part of a chat completion response the client reads.
type chatResponse struct {
	Choices []struct {
		Logprobs *struct {
			Content []struct {
				candidate

				TopLogprobs []candidate `json:"top_logprobs"`
			} `json:"content"`
		} `json:"logprobs"`
	} `json:"choices"`
}

// Provide wires the module. It needs the *http.Client of stdhttpclientfx.
func Provide() fx.Option {
	return stdfx.NoProvideZapEnvCfgModule[Config]("stdbifrost", fx.Provide(New))
}
