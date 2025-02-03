package stdpgxfx_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

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
)

func TestProvideNoDeriver(t *testing.T) {
	_, shared := setup(t)

	var res struct {
		fx.In
		DB0 *sql.DB `name:"db0"`
		DB1 *sql.DB `name:"db1"`
	}

	app := fxtest.New(t, shared,
		stdpgxfx.Provide("db0", "db1"),
		fx.Populate(&res))
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	require.NotNil(t, res.DB0)
	require.NotNil(t, res.DB1)
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
		stdpgxfx.Provide("db0", "db1"),
		stdpgxfx.ProvideDeriver("db0", func(base *pgxpool.Config) *pgxpool.Config {
			deriver0Name = "db0"
			return base
		}),
		stdpgxfx.ProvideDeriver("db1", func(base *pgxpool.Config) *pgxpool.Config {
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
		stdpgxfx.TestProvide(t, "db0", "db1"),
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

	app := fxtest.New(t, shared,
		stdpgxfx.TestProvide(t, "db0", "db1"),
		// provide the pgtesdb migrator.
		fx.Provide(func() pgtestdb.Migrator {
			return goosemigrator.New(filepath.Join("testdata", "migrations1"))
		}),
		// provide the mimplementation of our test migrator
		fx.Provide(stdpgxfx.NewPgtestdbTestMigrator),
		// imagine an after migration user, such that the deriver can see it.
		fx.Supply(&pgtestdb.Role{
			Username: "postgres",
			Password: "postgres",
		}),
		// derived databases should get the "after" role
		stdpgxfx.ProvideDeriver("db0", func(base *pgxpool.Config) *pgxpool.Config {
			db0DerivedUser = base.ConnConfig.User
			db0DerivedPassword = base.ConnConfig.Password
			return base
		}),
		stdpgxfx.ProvideDeriver("db1", func(base *pgxpool.Config) *pgxpool.Config {
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

func setup(tb testing.TB) (context.Context, fx.Option) {
	ctx, cancel := context.WithCancel(context.Background())
	tb.Cleanup(cancel)
	return ctx, fx.Options(
		stdzapfx.Fx(),
		stdzapfx.TestProvide(tb),
		stdenvcfg.ProvideEnvironment(map[string]string{
			"STDPGX_MAIN_DATABASE_URL": "postgresql://postgres:postgres@localhost:5440/postgres",
		}),
	)
}
