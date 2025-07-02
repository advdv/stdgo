package stdpgxfx_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/advdv/stdgo/fx/stdawsfx"
	"github.com/advdv/stdgo/fx/stdpgxfx"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestProvideNoDeriver(t *testing.T) {
	_, shared := setup(t)

	var res struct {
		fx.In
		DB0 *sql.DB `name:"db0"`
		DB1 *sql.DB `name:"db1"`
	}

	app := fxtest.New(t, shared,
		stdpgxfx.Provide(stdpgxfx.NewStandardDriver(), "db0", "db1"),
		fx.Populate(&res))
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	require.NotNil(t, res.DB0)
	require.NotNil(t, res.DB1)
}

func TestNoticeLogging(t *testing.T) {
	ctx, shared := setup(t)

	var db *sql.DB
	var obs *observer.ObservedLogs

	app := fxtest.New(t, shared, stdpgxfx.TestProvide(t, pgtestdb.NoopMigrator{}, stdpgxfx.NewStandardDriver(), "rw"), fx.Populate(&db, &obs))
	app.RequireStart()
	t.Cleanup(app.RequireStop)
	require.NotNil(t, db)

	_, err := db.ExecContext(ctx, `DO language plpgsql $$
BEGIN
  raise info 'information message' ;
  raise warning 'warning message';
  raise notice 'notice message';
END
$$;`)
	require.NoError(t, err)

	require.Equal(t, 1, obs.FilterMessage("notice: information message").Len())
	require.Equal(t, 1, obs.FilterMessage("notice: warning message").Len())
	require.Equal(t, 1, obs.FilterMessage("notice: notice message").Len())
}

func TestIamAuth(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "A")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "B")

	var db *sql.DB
	var pcfg *pgxpool.Config
	var obs *observer.ObservedLogs
	app := fxtest.New(t,
		stdawsfx.Provide(),
		stdzapfx.Fx(),
		stdzapfx.TestProvide(t),
		stdenvcfg.ProvideExplicitEnvironment(map[string]string{
			"STDPGX_MAIN_DATABASE_URL": "postgresql://postgres:postgres@localhost:5440/postgres",
			"STDPGX_IAM_AUTH_REGION":   "eu-central-1",
			"STDZAP_LEVEL":             "debug",
		}),

		stdpgxfx.TestProvide(t, pgtestdb.NoopMigrator{}, stdpgxfx.NewStandardDriver(), "rw"),
		fx.Populate(&db, &pcfg, &obs))
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	require.NotNil(t, pcfg.BeforeConnect)

	// cannot exactly assert it, but log with error should make it work.
	var res int
	err := db.QueryRowContext(t.Context(), `SELECT 1+2`).Scan(&res)
	require.ErrorContains(t, err, "password authentication failed")
	require.Equal(t, 1, obs.FilterMessage("building IAM auth token").Len())
}

func TestProvideWithDeriver(t *testing.T) {
	_, shared := setup(t)

	var res struct {
		fx.In
		DB0 *sql.DB `name:"db0"`
		DB1 *sql.DB `name:"db1"`
	}

	var deriver0Name string
	var deriver1Name string

	app := fxtest.New(t, shared,
		stdpgxfx.Provide(stdpgxfx.NewStandardDriver(), "db0", "db1"),
		stdpgxfx.ProvideDeriver("db0", func(logs *zap.Logger, base *pgxpool.Config) *pgxpool.Config {
			deriver0Name = "db0"
			return base
		}),
		stdpgxfx.ProvideDeriver("db1", func(logs *zap.Logger, base *pgxpool.Config) *pgxpool.Config {
			deriver1Name = "db1"
			return base
		}),

		fx.Populate(&res))
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	require.NotNil(t, res.DB0)
	require.NotNil(t, res.DB1)
	require.Equal(t, "db0", deriver0Name)
	require.Equal(t, "db1", deriver1Name)
}

func TestProvideTest(t *testing.T) {
	_, shared := setup(t)

	var res struct {
		fx.In
		DB0 *sql.DB `name:"db0"`
		DB1 *sql.DB `name:"db1"`
	}

	app := fxtest.New(t, shared,
		stdpgxfx.TestProvide(t, pgtestdb.NoopMigrator{}, stdpgxfx.NewStandardDriver(), "db0", "db1"),
		fx.Populate(&res))
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	require.NotNil(t, res.DB0)
	require.NotNil(t, res.DB1)
}

func TestProvideTestWithMigrator(t *testing.T) {
	ctx, shared := setup(t)

	var res struct {
		fx.In
		DB0 *sql.DB `name:"db0"`
		DB1 *sql.DB `name:"db1"`
	}

	var db1DerivedUser string
	var db1DerivedPassword string
	var db0DerivedUser string
	var db0DerivedPassword string

	mig := goosemigrator.New(filepath.Join("testdata", "migrations1"))

	app := fxtest.New(t, shared,
		stdpgxfx.TestProvide(t, mig, stdpgxfx.NewStandardDriver(), "db0", "db1"),
		// imagine an after migration user, such that the deriver can see it.
		fx.Supply(&pgtestdb.Role{
			Username: "postgres",
			Password: "postgres",
		}),
		// derived databases should get the "after" role
		stdpgxfx.ProvideDeriver("db0", func(_ *zap.Logger, base *pgxpool.Config) *pgxpool.Config {
			db0DerivedUser = base.ConnConfig.User
			db0DerivedPassword = base.ConnConfig.Password
			return base
		}),
		stdpgxfx.ProvideDeriver("db1", func(_ *zap.Logger, base *pgxpool.Config) *pgxpool.Config {
			db1DerivedUser = base.ConnConfig.User
			db1DerivedPassword = base.ConnConfig.Password
			return base
		}),

		fx.Populate(&res))
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	require.NotNil(t, res.DB0)
	require.NotNil(t, res.DB1)

	_, err := res.DB0.ExecContext(ctx, `INSERT INTO foo(id) VALUES($1)`, uuid.New())
	require.NoError(t, err)
	_, err = res.DB1.ExecContext(ctx, `INSERT INTO foo(id) VALUES($1)`, uuid.New())
	require.NoError(t, err)

	require.Equal(t, "postgres", db0DerivedUser)
	require.Equal(t, "postgres", db0DerivedPassword)
	require.Equal(t, "postgres", db1DerivedUser)
	require.Equal(t, "postgres", db1DerivedPassword)
}

func TestProvidePgxPoolTestWithMigrator(t *testing.T) {
	ctx, shared := setup(t)

	var res struct {
		fx.In
		DB0 *pgxpool.Pool `name:"db0"`
		DB1 *pgxpool.Pool `name:"db1"`
	}

	var db1DerivedUser string
	var db1DerivedPassword string
	var db0DerivedUser string
	var db0DerivedPassword string

	mig := goosemigrator.New(filepath.Join("testdata", "migrations1"))

	app := fxtest.New(t, shared,
		stdpgxfx.TestProvide(t, mig, stdpgxfx.NewPgxV5Driver(), "db0", "db1"),
		// imagine an after migration user, such that the deriver can see it.
		fx.Supply(&pgtestdb.Role{
			Username: "postgres",
			Password: "postgres",
		}),
		// derived databases should get the "after" role
		stdpgxfx.ProvideDeriver("db0", func(_ *zap.Logger, base *pgxpool.Config) *pgxpool.Config {
			db0DerivedUser = base.ConnConfig.User
			db0DerivedPassword = base.ConnConfig.Password
			return base
		}),
		stdpgxfx.ProvideDeriver("db1", func(_ *zap.Logger, base *pgxpool.Config) *pgxpool.Config {
			db1DerivedUser = base.ConnConfig.User
			db1DerivedPassword = base.ConnConfig.Password
			return base
		}),

		fx.Populate(&res))
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	require.NotNil(t, res.DB0)
	require.NotNil(t, res.DB1)

	_, err := res.DB0.Exec(ctx, `INSERT INTO foo(id) VALUES($1)`, uuid.New())
	require.NoError(t, err)
	_, err = res.DB1.Exec(ctx, `INSERT INTO foo(id) VALUES($1)`, uuid.New())
	require.NoError(t, err)

	require.Equal(t, "postgres", db0DerivedUser)
	require.Equal(t, "postgres", db0DerivedPassword)
	require.Equal(t, "postgres", db1DerivedUser)
	require.Equal(t, "postgres", db1DerivedPassword)
}

func setup(tb testing.TB) (context.Context, fx.Option) {
	return tb.Context(), fx.Options(
		stdzapfx.Fx(),
		stdzapfx.TestProvide(tb),
		stdenvcfg.ProvideExplicitEnvironment(map[string]string{
			"STDPGX_MAIN_DATABASE_URL": "postgresql://postgres:postgres@localhost:5440/postgres",
		}),
	)
}
