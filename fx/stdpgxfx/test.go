package stdpgxfx

import (
	"testing"

	"github.com/advdv/stdgo/stdfx"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

type testingPoolConfigParams struct {
	fx.In
	Cfg      Config
	Logs     *zap.Logger
	Migrator TestMigrator `optional:"true"`
}

// testingPoolConfigProvider is a provider factory that allows for a TestMigrator implementation
// to take the pool configuration, use it in any way, and return a modified pool configuration.
func testingPoolConfigProvider(
	tb testing.TB,
) func(p testingPoolConfigParams) (*pgxpool.Config, error) {
	tb.Helper()
	return func(p testingPoolConfigParams) (*pgxpool.Config, error) {
		pool, err := NewPoolConfig(p.Cfg, p.Logs)
		if err != nil {
			return nil, err
		}

		if p.Migrator == nil {
			return pool, nil
		}

		return p.Migrator.Migrate(tb, p.Cfg, pool)
	}
}

// TestProvide provides the package's components as an fx module with a setup more useful for testing.
func TestProvide(tb testing.TB, derivedPoolNames ...string) fx.Option {
	tb.Helper()

	return stdfx.ZapEnvCfgModule[Config]("stdpgx",
		New,
		fx.Provide(fx.Private, testingPoolConfigProvider(tb)),
		// re-export as an unamed pool, that is more common in testing.
		fx.Provide(
			fx.Annotate(func(p *pgxpool.Pool) *pgxpool.Pool {
				return p
			}, fx.ParamTags(`name:"rw"`))),
		// included derived pools
		withDerivedPools(derivedPoolNames...),
	)
}
