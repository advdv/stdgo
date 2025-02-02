package stdpgxfx_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/advdv/stdgo/fx/stdpgxfx"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdlo"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap/zaptest/observer"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPgxPoolWithValidShutdown(t *testing.T) {
	t.Setenv("STDPGX_RW_DATABASE_URL", "postgresql://postgres:postgres@localhost:5440/postgres")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	var res struct {
		fx.In
		RW  *pgxpool.Pool `name:"rw"`
		Obs *observer.ObservedLogs
	}

	app := fxtest.New(t,
		stdzapfx.Fx(),
		stdzapfx.TestProvide(t),
		stdpgxfx.Provide(),
		fx.Populate(&res))
	app.RequireStart()

	require.NotNil(t, res.RW)
	require.NoError(t, res.RW.Ping(ctx))

	app.RequireStop()

	assert.Equal(t, 1, res.Obs.FilterMessage("initialized connection config").Len())
	assert.Equal(t, 1, res.Obs.FilterMessage("connection pool was closed").Len())
}

func TestPgxPoolWithBlockedShutdown(t *testing.T) {
	ctx := setup(t)

	var res struct {
		fx.In
		RW  *pgxpool.Pool `name:"rw"`
		Obs *observer.ObservedLogs
	}

	app := fxtest.New(t,
		stdzapfx.Fx(),
		stdzapfx.TestProvide(t),
		stdpgxfx.Provide(),
		fx.Decorate(func(c stdpgxfx.Config) stdpgxfx.Config {
			c.PoolCloseTimeout = time.Millisecond * 10

			return c
		}),
		fx.Populate(&res))
	app.RequireStart()

	require.NotNil(t, res.RW)
	require.NoError(t, res.RW.Ping(ctx))

	txOpen := make(chan struct{})
	go func() {
		ctx := context.Background()
		tx := stdlo.Must1(res.RW.Begin(ctx))
		close(txOpen)
		time.Sleep(time.Second * 10)
		stdlo.Must0(tx.Rollback(ctx))
	}()

	// wait for the tx to open, or it races to close the pool before the tx is open
	<-txOpen

	require.ErrorIs(t, app.Stop(ctx), context.DeadlineExceeded)
	assert.Equal(t, 1, res.Obs.FilterMessage("initialized connection config").Len())
	assert.Equal(t, 1, res.Obs.FilterMessage("failed to close connection pool in time").Len())
}

func TestPgxTestProvideWithDerived(t *testing.T) {
	ctx := setup(t)
	var rw *pgxpool.Pool
	var ro *pgxpool.Pool
	var r1 *pgxpool.Pool
	var r2 *pgxpool.Pool

	var derivedHookCalled bool
	app := fxtest.New(t,
		fx.Populate(&rw),
		fx.Populate(fx.Annotate(&ro, fx.ParamTags(`name:"ro"`))),
		fx.Populate(fx.Annotate(&r1, fx.ParamTags(`name:"r1"`))),
		fx.Populate(fx.Annotate(&r2, fx.ParamTags(`name:"r2"`))),
		stdzapfx.Fx(),
		stdzapfx.TestProvide(t),
		stdpgxfx.TestProvide(t, "ro", "r1", "r2"),

		// imagine a derived provider with an extra connection pool hook
		stdpgxfx.ProvideDeriver("ro", func(base *pgxpool.Config) *pgxpool.Config {
			base.BeforeConnect = func(ctx context.Context, cc *pgx.ConnConfig) error {
				derivedHookCalled = true
				return nil
			}
			return base
		}),
		// some more derived pools that do nothing
		stdpgxfx.ProvideDeriver("r1", func(base *pgxpool.Config) *pgxpool.Config { return base }),
		stdpgxfx.ProvideDeriver("r2", func(base *pgxpool.Config) *pgxpool.Config { return base }),
	)

	app.RequireStart()
	t.Cleanup(app.RequireStop)

	require.NoError(t, rw.Ping(ctx))

	require.False(t, derivedHookCalled)
	require.NoError(t, ro.Ping(ctx))
	require.True(t, derivedHookCalled)
}

func TestPgxTestProvideWithMigrator(t *testing.T) {
	ctx := setup(t)

	var res struct {
		fx.In
		Web *pgxpool.Pool
		Sys *pgxpool.Pool `name:"sys"`
	}

	var sysDerivedUser string
	var sysDerivedPassword string

	app := fxtest.New(t,
		fx.Populate(&res),

		// provide the pgtesdb migrator.
		fx.Provide(func() pgtestdb.Migrator {
			return goosemigrator.New(filepath.Join("testdata", "migrations1"))
		}),
		// provide the mimplementation of our test migrator
		fx.Provide(stdpgxfx.NewPgtestdbTestMigrator),

		// imagine an after migration user, such that the deriver can see it.
		fx.Supply(&stdpgxfx.AfterMigrateRole{
			User:     "postgres",
			Password: "postgres",
		}),

		stdzapfx.Fx(),
		stdzapfx.TestProvide(t),
		stdpgxfx.TestProvide(t, "sys"),

		// test how a deriver can even further customize the user/password after migration.
		stdpgxfx.ProvideDeriver("sys", func(base *pgxpool.Config) *pgxpool.Config {
			sysDerivedUser = base.ConnConfig.User
			sysDerivedPassword = base.ConnConfig.Password
			return base
		}),
	)

	app.RequireStart()
	t.Cleanup(app.RequireStop)

	require.NotNil(t, res.Web)
	require.Equal(t, "postgres", sysDerivedUser)
	require.Equal(t, "postgres", sysDerivedPassword)

	rows, err := res.Web.Query(ctx, `SELECT * FROM foo;`)
	require.NoError(t, err)
	rows.Close()
}

func setup(tb testing.TB) context.Context {
	tb.Helper()
	tb.Setenv("STDPGX_RW_DATABASE_URL", "postgresql://postgres:postgres@localhost:5440/postgres")
	ctx, cancel := context.WithCancel(context.Background())
	tb.Cleanup(cancel)
	return ctx
}
