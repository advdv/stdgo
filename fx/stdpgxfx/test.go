package stdpgxfx

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/advdv/stdgo/stdfx"
	"github.com/peterldowns/pgtestdb"
	"go.uber.org/fx"
)

// testPoolMaxConns is the Config.PoolMaxConns that TestProvide uses when it is
// unset. Without a cap, pgxpool allows as many connections as the machine has
// CPUs, per pool; tests run in parallel, every test package of a run shares one
// PostgreSQL server (stdtestcontainer.Postgres), and that server refuses
// connections beyond its max_connections. A fixed cap keeps the server's peak
// bounded however many CPUs run the tests, and pools open connections only as
// they need them.
const testPoolMaxConns = 10

// an fx.In that is build by composing.
type testConfigProviderParams struct {
	Params

	Migrator TestMigrator
}

// newTestConfigProvider wraps the non-testing config provider such that it can
// first perform migration and completely replace the config.
func newTestConfigProvider(tb testing.TB) func(testConfigProviderParams) (Result, error) {
	return func(p testConfigProviderParams) (res Result, err error) {
		baseRes, err := New(p.Params)
		if err != nil {
			return res, fmt.Errorf("failed to create base config: %w", err)
		}

		if p.Migrator != nil {
			baseRes.PoolConfig, err = p.Migrator.Migrate(tb, p.Config, baseRes.PoolConfig)
			if err != nil {
				return res, fmt.Errorf("failed to migrate: %w", err)
			}
		}

		// The migrator may replace the config that New capped. Every pool derives
		// from this one, so the cap holds for each of them.
		maxConns := p.Config.PoolMaxConns
		if maxConns == 0 {
			maxConns = testPoolMaxConns
		}

		applyPoolMaxConns(baseRes.PoolConfig, maxConns)

		return baseRes, nil
	}
}

// TestProvide provides the package's components as an fx module with a setup more useful for testing.
func TestProvide[DBT any](
	tb testing.TB, mig pgtestdb.Migrator, drv Driver[DBT], mainPoolName string, derivedPoolNames ...string,
) fx.Option {
	tb.Helper()

	return stdfx.ZapEnvCfgModule[Config]("stdpgx",
		// a wrapped config provider that migrates before providing the config.
		newTestConfigProvider(tb),
		// for some dynamically created provides are dependant on this.
		fx.Provide(func() Driver[DBT] { return drv }),
		// setup dependecies for the test config provider.
		fx.Provide(func() pgtestdb.Migrator { return mig }),
		fx.Provide(NewPgtestdbTestMigrator),
		provideFinalPoolConfig(mainPoolName),
		// provide the "main" db, wrapped with migrating logic.
		fx.Provide(fx.Annotate(
			newDB[DBT],
			fx.ParamTags("", `name:"`+mainPoolName+`"`),
			fx.ResultTags(`name:"`+mainPoolName+`"`))),
		// re-export as an unamed db, that is more common in testing.
		fx.Provide(
			fx.Annotate(func(p *sql.DB) *sql.DB {
				return p
			}, fx.ParamTags(`name:"`+mainPoolName+`"`))),
		// included derived pools
		withDerivedPools(drv, mainPoolName, derivedPoolNames...),
	)
}
