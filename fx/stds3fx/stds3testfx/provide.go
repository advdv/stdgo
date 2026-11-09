// Package stds3testfx provides the stds3fx client for tests, on an S3
// server that is started for the tests, so they need no Docker Compose or other
// prior setup.
//
// The server is shared by all tests of a `go test` run (see
// [stdtestcontainer.S3]). Every fx app still gets its own buckets, created
// when it starts and removed when it stops. Only test code should import this
// package: it pulls in testcontainers-go and the Docker client.
package stds3testfx

import (
	"maps"
	"testing"

	"github.com/advdv/stdgo/fx/stds3fx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/advdv/stdgo/stdtestcontainer"
	"go.uber.org/fx"
)

// Provide works like [stds3fx.Provide], but connects to an S3 server that
// is started for the tests from image (see [stdtestcontainer.S3]), instead of
// the one in STDS3_ENDPOINT, which it sets for the client along with the
// server's credentials. It creates and removes random buckets unless the
// environment sets STDS3_CREATE_AND_USE_RANDOM_BUCKETS or
// STDS3_AUTO_REMOVE_BUCKETS itself. STDS3_BUCKETS stays the caller's to
// set.
func Provide(tb testing.TB, image string) fx.Option {
	tb.Helper()

	return fx.Module("stds3test",
		// Scoped to this module, so only the client sees the server.
		fx.Decorate(func(env stdenvcfg.Environment) stdenvcfg.Environment {
			out := stdenvcfg.Environment{
				"STDS3_CREATE_AND_USE_RANDOM_BUCKETS": "true",
				"STDS3_AUTO_REMOVE_BUCKETS":           "true",
			}
			maps.Copy(out, env)
			out["STDS3_ENDPOINT"] = stdtestcontainer.S3(tb, image)
			out["STDS3_ACCESS_KEY"] = stdtestcontainer.S3AccessKey
			out["STDS3_SECRET_KEY"] = stdtestcontainer.S3SecretKey

			return out
		}),
		stds3fx.Provide(),
	)
}
