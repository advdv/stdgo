package stdenttxfx_test

import (
	"context"
	"testing"

	"entgo.io/ent/dialect"
	"github.com/advdv/stdgo/fx/stdenttxfx"
	"github.com/advdv/stdgo/fx/stdenttxfx/testdata/model"
	"github.com/advdv/stdgo/fx/stdpgxfx"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdent"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/peterldowns/pgtestdb"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestSetupRWOnly(t *testing.T) {
	t.Parallel()

	ctx, rw := setupRWOnly(t)
	require.NotNil(t, ctx)
	require.NotNil(t, rw)

	require.False(t, rw.IsReadOnly())
}

func TestUserRWOnly(t *testing.T) {
	t.Parallel()

	var obs *observer.ObservedLogs

	ctx, rw := setupRWOnly(t, &obs)

	require.NoError(t, stdent.Transact0(ctx, rw, func(_ context.Context, _ *model.Tx) error {
		return nil
	}))

	// the BeginHook supplied via fx must still fire when only the rw transactor
	// is wired up — this is the core parity guarantee of ProvideRWOnly.
	require.Equal(t, 1, obs.FilterMessage("hook called").Len())
}

func TestRWOnlyDoesNotProvideRO(t *testing.T) {
	t.Parallel()

	// When only ProvideRWOnly is wired, no `name:"ro"` transactor exists in the
	// graph. Asking for one must cause the fx app to fail to start.
	var deps struct {
		fx.In
		RO *stdent.Transactor[*model.Tx] `name:"ro"`
	}

	app := fx.New(
		fx.NopLogger,
		stdenvcfg.ProvideExplicitEnvironment(map[string]string{
			"STDPGX_MAIN_DATABASE_URL": "postgresql://postgres:postgres@localhost:5440/postgres",
		}),
		stdzapfx.Fx(),
		stdzapfx.TestProvide(t),
		stdpgxfx.TestProvide(t, pgtestdb.NoopMigrator{}, stdpgxfx.NewStandardDriver(), "rw"),
		stdenttxfx.TestProvideRWOnly("testapp-rwonly", "postgres", "postgres",
			func(driver dialect.Driver) *model.Client { return model.NewClient(model.Driver(driver)) }),
		fx.Provide(testHook),
		fx.Populate(&deps),
	)

	require.Error(t, app.Err())
}

func setupRWOnly(tb testing.TB, other ...any) (context.Context, *stdent.Transactor[*model.Tx]) {
	tb.Helper()

	var deps struct {
		fx.In
		*zap.Logger
		RW *stdent.Transactor[*model.Tx] `name:"rw"`
	}

	app := fxtest.New(tb,
		stdenvcfg.ProvideExplicitEnvironment(map[string]string{
			"STDPGX_MAIN_DATABASE_URL": "postgresql://postgres:postgres@localhost:5440/postgres",
		}),
		stdzapfx.Fx(),
		stdzapfx.TestProvide(tb),
		// note: only the "rw" pool is declared — no "ro" derived pool.
		stdpgxfx.TestProvide(tb, pgtestdb.NoopMigrator{}, stdpgxfx.NewStandardDriver(), "rw"),
		stdenttxfx.TestProvideRWOnly("testapp-rwonly", "postgres", "postgres",
			func(driver dialect.Driver) *model.Client { return model.NewClient(model.Driver(driver)) }),
		fx.Provide(testHook),
		fx.Populate(&deps),
		fx.Populate(other...),
	)

	app.RequireStart()
	tb.Cleanup(app.RequireStop)

	ctx := tb.Context()
	ctx = stdctx.WithLogger(ctx, deps.Logger)

	return ctx, deps.RW
}
