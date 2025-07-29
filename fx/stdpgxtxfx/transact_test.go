package stdpgxtxfx_test

import (
	"context"
	"strings"
	"testing"

	"github.com/advdv/stdgo/fx/stdpgxfx"
	"github.com/advdv/stdgo/fx/stdpgxtxfx"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/advdv/stdgo/stdtx"
	"github.com/advdv/stdgo/stdtx/stdtxpgxv5"
	"github.com/jackc/pgx/v5"
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
}

func testHook(logs *zap.Logger) (out struct {
	fx.Out
	stdtxpgxv5.TxBeginSQLFunc
},
) {
	out.TxBeginSQLFunc = func(_ context.Context, b *strings.Builder, _ pgx.Tx) (*strings.Builder, error) {
		logs.Info("hook called")
		return b, nil
	}

	return
}

func setup(tb testing.TB, other ...any) (context.Context, *stdtx.Transactor[pgx.Tx], *stdtx.Transactor[pgx.Tx]) {
	var deps struct {
		fx.In
		*zap.Logger
		RW *stdtx.Transactor[pgx.Tx] `name:"rw"`
		RO *stdtx.Transactor[pgx.Tx] `name:"ro"`
	}

	app := fxtest.New(tb,
		stdenvcfg.ProvideExplicitEnvironment(map[string]string{
			"STDPGX_MAIN_DATABASE_URL": "postgresql://postgres:postgres@localhost:5440/postgres",
		}),
		stdzapfx.Fx(),
		stdzapfx.TestProvide(tb),
		stdpgxfx.TestProvide(tb, pgtestdb.NoopMigrator{}, stdpgxfx.NewPgxV5Driver(), "rw", "ro"),
		stdpgxtxfx.TestProvide("testapp", "postgres", "postgres"),
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
