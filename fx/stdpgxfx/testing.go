package stdpgxfx

import (
	"database/sql"
	"testing"

	"github.com/advdv/stdgo/stdfx"
	"github.com/advdv/stdgo/stdpgtest"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// PgTestDBHook can be provided to modify the testdb configuration before it's being used
// in creating a pgx connection pool.
type PgTestDBHook func(*pgtestdb.Config)

type testingPoolConfigParams struct {
	fx.In
	Cfg          Config
	Logs         *zap.Logger
	Migrater     pgtestdb.Migrator `optional:"true"`
	PgTestDBHook PgTestDBHook      `optional:"true"`
}

// testingPoolConfigProvider is a provider factory that can optionally create an isolated and migrated testing
// database using the testdb package.
func testingPoolConfigProvider(tb testing.TB) func(p testingPoolConfigParams) (*pgxpool.Config, error) {
	tb.Helper()

	return func(p testingPoolConfigParams) (*pgxpool.Config, error) {
		if p.Migrater != nil {
			p.Logs.Info("non-nill migrater, creating migrated test database")

			testCfg := stdpgtest.NewPgxTestDB(tb, p.Migrater, p.Cfg.RWDatabaseURL, nil)
			if p.PgTestDBHook != nil {
				p.PgTestDBHook(testCfg)
			}

			p.Cfg.RWDatabaseURL = testCfg.URL()
		}

		return NewPoolConfig(p.Cfg, p.Logs)
	}
}

// TestProvide provides the package's components as an fx module with a setup more useful for testing.
func TestProvide(tb testing.TB) fx.Option {
	tb.Helper()

	return stdfx.ZapEnvCfgModule[Config]("stdpgx",
		New,
		fx.Provide(fx.Private, testingPoolConfigProvider(tb)),
		// re-export as an unamed pool, that is more common in testing.
		fx.Provide(
			fx.Annotate(func(p *pgxpool.Pool) *pgxpool.Pool {
				return p
			}, fx.ParamTags(`name:"rw"`))),
	)
}

// SnapshotProvide provides a snapshot migrater.
func SnapshotProvide(filename string) fx.Option {
	return fx.Provide(func() pgtestdb.Migrator {
		return stdpgtest.SnapshotMigrater[*sql.DB](filename)
	})
}
