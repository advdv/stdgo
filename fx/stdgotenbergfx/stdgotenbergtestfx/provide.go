// Package stdgotenbergtestfx provides the stdgotenbergfx client for tests, on a
// Gotenberg server that is started for the tests ([stdtestcontainer.Gotenberg]),
// so they need no Docker Compose or other prior setup. Only test code should
// import this package: it pulls in testcontainers-go and the Docker client.
package stdgotenbergtestfx

import (
	"maps"
	"testing"

	"github.com/advdv/stdgo/fx/stdgotenbergfx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/advdv/stdgo/stdtestcontainer"
	"go.uber.org/fx"
)

// Provide works like [stdgotenbergfx.Provide], but connects to a server that is
// started for the tests from image, and shared by all tests of a `go test`
// run, instead of the one in STDGOTENBERG_URL. The server is on
// [stdtestcontainer.Network], where it reaches the requests stds3testfx's
// client pre-signs for services.
func Provide(tb testing.TB, image string) fx.Option {
	tb.Helper()

	return fx.Module("stdgotenbergtest",
		// Scoped to this module, so only the client sees the server.
		fx.Decorate(func(env stdenvcfg.Environment) stdenvcfg.Environment {
			out := maps.Clone(env)
			out["STDGOTENBERG_URL"] = stdtestcontainer.Gotenberg(tb, image).Endpoint

			return out
		}),
		stdgotenbergfx.Provide(),
	)
}
