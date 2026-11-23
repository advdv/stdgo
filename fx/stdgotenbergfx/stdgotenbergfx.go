// Package stdgotenbergfx provides a client of Gotenberg (https://gotenberg.dev),
// which converts documents as a service of its own, so no document is
// processed in Go.
//
// Gotenberg reads and writes the files itself, with requests that the caller
// pre-signs (stds3fx): it downloads its input (downloadFrom) and uploads its
// output (its webhook), so file contents pass through neither the service nor
// its workers. Run Gotenberg with WEBHOOK_ENABLE_SYNC_MODE, so a conversion
// returns once the upload is done.
//
// Gotenberg answers a conversion the same whether its upload succeeded or
// not, and reports why only to its events URL: callers check that the output
// arrived.
package stdgotenbergfx

import (
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"

	"github.com/advdv/stdgo/fx/stds3fx"
	"github.com/advdv/stdgo/stdfx"
	"github.com/carlmjohnson/requests"
	"github.com/cockroachdb/errors"
	"go.uber.org/fx"
)

// ErrRejected reports that Gotenberg refused a conversion, such as of a file
// it cannot download; sending it again fails the same way.
var ErrRejected = errors.New("stdgotenbergfx: rejected")

// Config configures the module, from the environment with the STDGOTENBERG_
// prefix.
type Config struct {
	// URL is where the service reaches Gotenberg's API.
	URL string `env:"URL,required"`
}

// eventsURL is where Gotenberg reports the outcome of each conversion, as a
// JSON POST; it refuses a webhook request without one. The client reads no
// events: it points Gotenberg at its own health route, from inside its
// container, which refuses the POST at once, and Gotenberg only logs that.
//
// It assumes Gotenberg listens on its default port, 3000 (no API_PORT), and
// may send webhooks to loopback (no WEBHOOK_DENY_PRIVATE_IPS, and no allow or
// deny list that excludes it). Port mappings outside the container do not
// matter. When an assumption fails, conversions still succeed, but each waits
// out Gotenberg's retries of the event, about 15 seconds by default.
const eventsURL = "http://127.0.0.1:3000/health"

// Client converts documents with Gotenberg. It is safe for concurrent use.
type Client struct {
	cfg  Config
	http *http.Client
}

// New builds the client, which sends its requests with client, such as that
// of stdhttpclientfx.
func New(cfg Config, client *http.Client) *Client {
	return &Client{cfg: cfg, http: client}
}

// ConvertOffice has Gotenberg convert the office document that source
// downloads into a PDF with LibreOffice, and upload it with target, a
// pre-signed PUT. Source's response must name the file (Content-Disposition),
// whose extension tells Gotenberg the format. It returns once Gotenberg is
// done, but Gotenberg answers the same whether the upload succeeded or not:
// callers check the output.
//
// It fails with [ErrRejected] when Gotenberg refuses the request, such as when
// it cannot download source.
func (c *Client) ConvertOffice(ctx context.Context, source, target *stds3fx.PresignedRequest) error {
	sources, err := json.Marshal([]download{{URL: source.URL, ExtraHTTPHeaders: flatten(source.Headers)}})
	if err != nil {
		return errors.Wrap(err, "encode downloadFrom")
	}

	// Gotenberg sends these with the upload; the signature covers them.
	targetHeaders, err := json.Marshal(flatten(target.Headers))
	if err != nil {
		return errors.Wrap(err, "encode webhook headers")
	}

	err = requests.URL(c.cfg.URL).
		Client(c.http).
		Path("/forms/libreoffice/convert").
		Header("Gotenberg-Webhook-Url", target.URL).
		Header("Gotenberg-Webhook-Method", target.Method).
		Header("Gotenberg-Webhook-Extra-Http-Headers", string(targetHeaders)).
		Header("Gotenberg-Webhook-Events-Url", eventsURL).
		Config(requests.BodyMultipart("", func(form *multipart.Writer) error {
			return errors.Wrap(form.WriteField("downloadFrom", string(sources)), "write downloadFrom")
		})).
		CheckStatus(http.StatusNoContent).
		Fetch(ctx)

	var response *requests.ResponseError
	if errors.As(err, &response) && response.StatusCode >= 400 && response.StatusCode < 500 &&
		response.StatusCode != http.StatusTooManyRequests {
		return errors.Wrapf(errors.Mark(err, ErrRejected), "convert: status %d", response.StatusCode)
	}

	return errors.Wrap(err, "convert")
}

// download is an entry of Gotenberg's downloadFrom form field.
type download struct {
	URL              string            `json:"url"`
	ExtraHTTPHeaders map[string]string `json:"extraHttpHeaders,omitempty"`
}

// flatten turns headers into the map Gotenberg takes, with the first value
// of each.
func flatten(headers http.Header) map[string]string {
	out := make(map[string]string, len(headers))
	for name := range headers {
		out[name] = headers.Get(name)
	}

	return out
}

// Provide wires the module. It needs the *http.Client of stdhttpclientfx.
func Provide() fx.Option {
	return stdfx.NoProvideZapEnvCfgModule[Config]("stdgotenberg", fx.Provide(New))
}
