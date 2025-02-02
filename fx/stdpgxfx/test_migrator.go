package stdpgxfx

import (
	"fmt"
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"go.uber.org/fx"
)

// TestMigrator can be implemented and provided to migrate the database for tests.
type TestMigrator interface {
	Migrate(
		tb testing.TB,
		cfg Config,
		pcfg *pgxpool.Config,
	) (*pgxpool.Config, error)
}

// TestMigratorFunc makes it easy to implement the migrator.
type TestMigratorFunc func(
	tb testing.TB,
	cfg Config,
	pcfg *pgxpool.Config,
) (*pgxpool.Config, error)

func (f TestMigratorFunc) Migrate(
	tb testing.TB, cfg Config, pcfg *pgxpool.Config,
) (*pgxpool.Config, error) {
	return f(tb, cfg, pcfg)
}

// AfterMigrateRole can be optionally provided to further customize the role
// used to connect to the database that was just migrated. This is useful if
// the eventual role being used from the application is different from the
// migration (superuser) role and the testrole.
type AfterMigrateRole struct {
	User     string
	Password string
}

type PgtestdbTestMigratorParams struct {
	fx.In
	Migrator  pgtestdb.Migrator
	Role      *pgtestdb.Role    `optional:"true"`
	AfterRole *AfterMigrateRole `optional:"true"`
}

// NewPgtestdbTestMigrator implements the [TestMigrator] using the pgtestdb library.
func NewPgtestdbTestMigrator(params PgtestdbTestMigratorParams) TestMigrator {
	return TestMigratorFunc(func(
		tb testing.TB, _ Config, pcfg *pgxpool.Config,
	) (*pgxpool.Config, error) {
		urlParsed, err := url.Parse(pcfg.ConnString())
		if err != nil {
			tb.Fatalf("failed to parse conn string: %v", err)
		}

		// connect to the database to facilitate the migration instances using the
		// base pool connection.
		tcfg := pgtestdb.Custom(tb, pgtestdb.Config{
			DriverName: "pgx",
			User:       pcfg.ConnConfig.User,
			Password:   pcfg.ConnConfig.Password,
			Host:       pcfg.ConnConfig.Host,
			Database:   pcfg.ConnConfig.Database,
			Port:       fmt.Sprintf("%d", pcfg.ConnConfig.Port),
			Options:    urlParsed.RawQuery,
			TestRole:   params.Role,
		}, params.Migrator)

		// now that we have a database instance (created from the template). We return
		// a copy of the pool config such that it will connect to the instance instead.
		pcfg = pcfg.Copy()
		pcfg.ConnConfig.Database = tcfg.Database
		pcfg.ConnConfig.User = tcfg.User
		pcfg.ConnConfig.Password = tcfg.Password

		// in some cases it can be useful to connect even with another role to the database
		// instance that was just initialized.
		if params.AfterRole != nil {
			pcfg.ConnConfig.User = params.AfterRole.User
			pcfg.ConnConfig.Password = params.AfterRole.Password
		}

		return pcfg, nil
	})
}
