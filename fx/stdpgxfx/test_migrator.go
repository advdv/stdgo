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

// EndRole defines a type that can be provided if the role that actually connects
// to the database in tests is different from the migration role.
type EndRole struct {
	Username string
	Password string
}

type PgtestdbTestMigratorParams struct {
	fx.In
	Migrator pgtestdb.Migrator
	Role     *pgtestdb.Role `optional:"true"`
	EndRole  *EndRole       `optional:"true"`
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

		// we want to support the case where the actual role for tests is different from
		// the one used for migrations.
		if params.EndRole != nil {
			pcfg.ConnConfig.User = params.EndRole.Username
			pcfg.ConnConfig.Password = params.EndRole.Password
		} else {
			pcfg.ConnConfig.User = tcfg.User
			pcfg.ConnConfig.Password = tcfg.Password
		}

		return pcfg, nil
	})
}
