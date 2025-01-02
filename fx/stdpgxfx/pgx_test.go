package stdpgxfx_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdlo"
	"github.com/advdv/stdgo/stdpgxfx"
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

	go func() {
		ctx := context.Background()
		tx := stdlo.Must1(res.RW.Begin(ctx))
		time.Sleep(time.Second * 10)
		stdlo.Must0(tx.Rollback(ctx))
	}()

	require.ErrorIs(t, app.Stop(ctx), context.DeadlineExceeded)
	assert.Equal(t, 1, res.Obs.FilterMessage("initialized connection config").Len())
	assert.Equal(t, 1, res.Obs.FilterMessage("failed to close connection pool in time").Len())
}

func TestPgxTestProvide(t *testing.T) {
	ctx := setup(t)
	var rw *pgxpool.Pool

	app := fxtest.New(t,
		fx.Populate(&rw),

		stdzapfx.Fx(),
		stdzapfx.TestProvide(t),
		stdpgxfx.TestProvide(t))
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	require.NoError(t, rw.Ping(ctx))
}

func TestPgxTestProvideWithMigrater(t *testing.T) {
	ctx := setup(t)
	var rw *pgxpool.Pool

	newMigrater := func() pgtestdb.Migrator {
		return goosemigrator.New(filepath.Join("testdata", "migrations1"))
	}

	app := fxtest.New(t,
		fx.Populate(&rw),
		fx.Provide(newMigrater),

		stdzapfx.Fx(),
		stdzapfx.TestProvide(t),
		stdpgxfx.TestProvide(t))
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	require.NotNil(t, rw)

	rows, err := rw.Query(ctx, `SELECT * FROM foo;`)
	require.NoError(t, err)
	rows.Close()
}

func TestPgxTestProvideWithSnapshotMigrater(t *testing.T) {
	ctx := setup(t)

	var rw *pgxpool.Pool
	app := fxtest.New(t,
		fx.Populate(&rw),
		stdzapfx.Fx(),
		stdzapfx.TestProvide(t),
		stdpgxfx.TestProvide(t),
		stdpgxfx.SnapshotProvide("testdata/snapshot1.sql"),
	)

	app.RequireStart()
	t.Cleanup(app.RequireStop)

	rows, err := rw.Query(ctx, `SELECT * FROM foo;`)
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
