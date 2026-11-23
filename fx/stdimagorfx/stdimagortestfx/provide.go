// Package stdimagortestfx provides the stdimagorfx client for tests, on an
// Imagor server of the fx app's own, so they need no Docker Compose or other
// prior setup. Only test code should import this package: it pulls in
// testcontainers-go and the Docker client.
package stdimagortestfx

import (
	"context"
	"maps"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/advdv/stdgo/fx/stdimagorfx"
	"github.com/advdv/stdgo/fx/stds3fx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/advdv/stdgo/stdtestcontainer"
	"github.com/cockroachdb/errors"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/fx"
)

// Secret signs the tests' requests to Imagor; not a secret.
const Secret = "stdimagortest-secret" //nolint:gosec // signs test requests only

// startTimeout bounds pulling the image and waiting for the server to be
// ready.
const startTimeout = 2 * time.Minute

// Provide works like [stdimagorfx.Provide], but connects to an Imagor server
// that is started from image for the fx app, and stopped when tb ends, instead
// of the one in STDIMAGOR_URL. The server writes its results to the app's
// bucket with alias bucket, under prefix, on the S3 server that stds3testfx
// started from s3Image, which it reaches on [stdtestcontainer.Network]. It
// loads sources only from that server, as stds3fx's client pre-signs them for
// services.
//
// Each app gets its own server, since Imagor writes to one bucket, and each
// app has buckets of its own. It needs stds3testfx's client.
func Provide(tb testing.TB, image, s3Image, bucket, prefix string) fx.Option {
	tb.Helper()

	return fx.Module("stdimagortest",
		fx.Decorate(func(env stdenvcfg.Environment) stdenvcfg.Environment {
			out := maps.Clone(env)
			out["STDIMAGOR_SECRET"] = Secret
			// Replaced with the server's URL once the bucket is known.
			out["STDIMAGOR_URL"] = "http://stdimagortest.invalid"

			return out
		}),
		stdimagorfx.Provide(),
		fx.Decorate(func(cfg stdimagorfx.Config, storage *stds3fx.Client) (stdimagorfx.Config, error) {
			name, ok := storage.Bucket(bucket)
			if !ok {
				return cfg, errors.Errorf("no bucket with alias %q", bucket)
			}

			endpoint, err := start(tb, image, s3Image, name, prefix)
			cfg.URL = endpoint

			return cfg, err
		}),
	)
}

// start runs a server that writes its results to bucket under prefix, until
// tb ends.
func start(tb testing.TB, image, s3Image, bucket, prefix string) (string, error) {
	tb.Helper()

	s3Server, err := url.Parse(stdtestcontainer.S3(tb, s3Image).NetworkEndpoint)
	if err != nil {
		return "", errors.Wrap(err, "parse s3 endpoint")
	}

	ctx, cancel := context.WithTimeout(tb.Context(), startTimeout)
	defer cancel()

	ctr, err := testcontainers.Run(ctx, image,
		testcontainers.WithEnv(map[string]string{
			"IMAGOR_SECRET":                    Secret,
			"IMAGOR_RESULT_STORAGE_PATH_STYLE": "digest",
			// The pattern matches the host with its port.
			"HTTP_LOADER_ALLOWED_SOURCES":          s3Server.Host,
			"S3_RESULT_STORAGE_BUCKET":             bucket,
			"S3_RESULT_STORAGE_BASE_DIR":           strings.TrimSuffix(prefix, "/"),
			"S3_RESULT_STORAGE_ENDPOINT":           s3Server.String(),
			"S3_FORCE_PATH_STYLE":                  "1",
			"AWS_RESULT_STORAGE_REGION":            "us-east-1",
			"AWS_RESULT_STORAGE_ACCESS_KEY_ID":     stdtestcontainer.S3AccessKey,
			"AWS_RESULT_STORAGE_SECRET_ACCESS_KEY": stdtestcontainer.S3SecretKey,
		}),
		stdtestcontainer.WithNetwork(tb),
		testcontainers.WithExposedPorts("8000/tcp"),
		testcontainers.WithWaitStrategy(wait.ForHTTP("/healthcheck").WithPort("8000/tcp")),
	)
	testcontainers.CleanupContainer(tb, ctr)

	if err != nil {
		return "", errors.Wrap(err, "run imagor container")
	}

	endpoint, err := ctr.PortEndpoint(ctx, "8000/tcp", "http")

	return endpoint, errors.Wrap(err, "endpoint")
}
