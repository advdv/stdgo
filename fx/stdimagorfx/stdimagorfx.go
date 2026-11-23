// Package stdimagorfx provides a client of Imagor (https://docs.imagor.net),
// which transforms images as a service of its own, so no image is processed
// in Go.
//
// No image passes through the service or its workers. Imagor downloads its
// input with a request that the caller pre-signs (stds3fx), and writes its
// result to its result storage, at a key it derives from the request: run it
// with an S3 result storage and the "digest" path style. [Client.Render] asks
// for a result with a HEAD request, which returns no body, and returns that
// key; callers then move the result into place within object storage, and
// wait for it with [Client.Await], since Imagor writes it after it answers.
package stdimagorfx

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/advdv/stdgo/stdfx"
	"github.com/carlmjohnson/requests"
	"github.com/cockroachdb/errors"
	"github.com/cshum/imagor/imagorpath"
	"go.uber.org/fx"
)

// ErrRejected reports that Imagor refused a transformation, such as of a
// source it cannot download or decode; sending it again fails the same way.
var ErrRejected = errors.New("stdimagorfx: rejected")

// ErrNotDelivered reports that Imagor answered a transformation but its
// result did not arrive in object storage in time. Imagor writes it after it
// answers, and only logs when that fails.
var ErrNotDelivered = errors.New("stdimagorfx: result not delivered")

// Config configures the module, from the environment with the STDIMAGOR_
// prefix.
type Config struct {
	// URL is where the service reaches Imagor.
	URL string `env:"URL,required"`
	// Secret signs the requests to Imagor, which must have the same
	// IMAGOR_SECRET, so nobody else can have it transform images.
	Secret string `env:"SECRET,required"`
	// DeliveryTimeout is how long [Client.Await] waits for a result to arrive
	// in object storage after Imagor answered.
	DeliveryTimeout time.Duration `env:"DELIVERY_TIMEOUT" envDefault:"10s"`
}

// Client transforms images with Imagor. It is safe for concurrent use.
type Client struct {
	cfg    Config
	signer imagorpath.Signer
	http   *http.Client
}

// New builds the client, which sends its requests with client, such as that
// of stdhttpclientfx.
func New(cfg Config, client *http.Client) *Client {
	return &Client{cfg: cfg, signer: imagorpath.NewDefaultSigner(cfg.Secret), http: client}
}

// Render has Imagor transform as params describe, and returns the key, under
// Imagor's result storage base directory, that Imagor writes the result to: a
// hash of the request, as the "digest" path style makes it. The result
// arrives after Render returns; wait for it with [Client.Await].
//
// It fails with [ErrRejected] when Imagor refuses the request, such as when it
// cannot download or decode params.Image.
func (c *Client) Render(ctx context.Context, params imagorpath.Params) (string, error) {
	path := imagorpath.GeneratePath(params)

	err := requests.URL(strings.TrimSuffix(c.cfg.URL, "/") + "/" + c.signer.Sign(path) + "/" + path).
		Client(c.http).
		Head().
		Fetch(ctx)

	var response *requests.ResponseError
	if errors.As(err, &response) && response.StatusCode >= 400 && response.StatusCode < 500 &&
		response.StatusCode != http.StatusTooManyRequests {
		return "", errors.Wrapf(errors.Mark(err, ErrRejected), "render: status %d", response.StatusCode)
	}

	if err != nil {
		return "", errors.Wrap(err, "render")
	}

	return imagorpath.DigestResultStorageHasher.HashResult(imagorpath.Params{Path: path}), nil
}

// Await calls adopt, which moves a result into place and reports whether it
// was there, until it reports that it was, backing off between calls. It
// fails with [ErrNotDelivered] once the delivery timeout passes, and with
// adopt's error when adopt fails before then.
func (c *Client) Await(ctx context.Context, adopt func(context.Context) (bool, error)) error {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.DeliveryTimeout)
	defer cancel()

	for wait := 10 * time.Millisecond; ; wait = min(2*wait, time.Second) {
		done, err := adopt(ctx)

		switch {
		case err != nil && ctx.Err() == nil:
			return err
		case done:
			return nil
		case ctx.Err() != nil:
			return errors.Wrapf(ErrNotDelivered, "within %s", c.cfg.DeliveryTimeout)
		}

		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
	}
}

// Provide wires the module. It needs the *http.Client of stdhttpclientfx.
func Provide() fx.Option {
	return stdfx.NoProvideZapEnvCfgModule[Config]("stdimagor", fx.Provide(New))
}
