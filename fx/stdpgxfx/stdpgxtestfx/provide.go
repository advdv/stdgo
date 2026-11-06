// Package stdpgxtestfx provides stdpgxfx connection pools for tests, on a PostgreSQL server that
// is started for the tests, so they need no Docker Compose or other prior setup.
//
// The server is shared by all tests of a `go test` run (see [stdtestcontainer.Postgres]). Every
// fx app still gets its own database, cloned from a template that the migrator prepared once per
// run, and all named pools (e.g. "rw" and "ro") connect to that one database. Only test code
// should import this package: it pulls in testcontainers-go and the Docker client.
package stdpgxtestfx

import (
	"maps"
	"testing"

	"github.com/advdv/stdgo/fx/stdpgxfx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/advdv/stdgo/stdtestcontainer"
	"github.com/peterldowns/pgtestdb"
	"go.uber.org/fx"
)

// Provide works like [stdpgxfx.TestProvide], but connects to a PostgreSQL server that is started
// for the tests from image (e.g. "postgres:18"; see [stdtestcontainer.Postgres]), instead of the
// one in STDPGX_MAIN_DATABASE_URL, which it sets for the pools. To use a server of your own, call
// [stdpgxfx.TestProvide] instead and set that variable.
func Provide[DBT any](
	tb testing.TB,
	image string,
	mig pgtestdb.Migrator,
	drv stdpgxfx.Driver[DBT],
	mainPoolName string,
	derivedPoolNames ...string,
) fx.Option {
	tb.Helper()

	return fx.Module("stdpgxtest",
		// Scoped to this module, so only the pools see the server's URL.
		fx.Decorate(func(env stdenvcfg.Environment) stdenvcfg.Environment {
			out := stdenvcfg.Environment{}
			maps.Copy(out, env)
			out["STDPGX_MAIN_DATABASE_URL"] = stdtestcontainer.Postgres(tb, image)

			return out
		}),
		stdpgxfx.TestProvide(tb, mig, drv, mainPoolName, derivedPoolNames...),
	)
}
