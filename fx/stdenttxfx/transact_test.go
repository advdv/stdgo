package stdenttxfx_test

import (
	"context"
	"strings"
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
)

func TestSetup(t *testing.T) {
	t.Parallel()

	ctx, ro, rw := setup(t)
	require.NotNil(t, ctx)
	require.NotNil(t, ro)
	require.NotNil(t, rw)

	require.True(t, ro.IsReadOnly())
	require.False(t, rw.IsReadOnly())
}

func testHook(logs *zap.Logger) (out struct {
	fx.Out
	stdent.BeginHookFunc
},
) {
	out.BeginHookFunc = func(_ context.Context, b *strings.Builder, tx dialect.ExecQuerier) (*strings.Builder, error) {
		logs.Info("hook called")
		return b, nil
	}

	return
}

func setup(tb testing.TB, other ...any) (context.Context, *stdent.Transactor[*model.Tx], *stdent.Transactor[*model.Tx]) {
	var deps struct {
		fx.In
		*zap.Logger
		RW *stdent.Transactor[*model.Tx] `name:"rw"`
		RO *stdent.Transactor[*model.Tx] `name:"ro"`
	}

	app := fxtest.New(tb,
		stdenvcfg.ProvideExplicitEnvironment(map[string]string{
			"STDPGX_MAIN_DATABASE_URL": "postgresql://postgres:postgres@localhost:5440/postgres",
		}),
		stdzapfx.Fx(),
		stdzapfx.TestProvide(tb),
		stdpgxfx.TestProvide(tb, pgtestdb.NoopMigrator{}, stdpgxfx.NewStandardDriver(), "rw", "ro"),
		stdenttxfx.TestProvide("testapp", "postgres", "postgres",
			func(driver dialect.Driver) *model.Client { return model.NewClient(model.Driver(driver)) }),
		fx.Provide(testHook),
		fx.Populate(&deps),
		fx.Populate(other...),
	)

	app.RequireStart()
	tb.Cleanup(app.RequireStop)

	ctx := tb.Context()
	ctx = stdctx.WithLogger(ctx, deps.Logger)

	return ctx, deps.RO, deps.RW
}
