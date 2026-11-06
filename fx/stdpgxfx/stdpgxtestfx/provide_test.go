package stdpgxtestfx_test

import (
	"testing"

	"github.com/advdv/stdgo/fx/stdpgxfx"
	"github.com/advdv/stdgo/fx/stdpgxfx/stdpgxtestfx"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"
)

type pools struct {
	fx.In

	RW *pgxpool.Pool `name:"rw"`
	RO *pgxpool.Pool `name:"ro"`
}

// setup starts an app with a read-write and a read-only pool, as services typically have.
func setup(tb testing.TB) pools {
	tb.Helper()

	var deps pools

	app := fxtest.New(tb,
		stdzapfx.Fx(),
		stdzapfx.TestProvide(tb),
		stdenvcfg.ProvideExplicitEnvironment(nil),
		stdpgxfx.ProvideDeriver("ro", func(_ *zap.Logger, base *pgxpool.Config) *pgxpool.Config {
			base.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
			return base
		}),
		stdpgxtestfx.Provide(tb, "postgres:18", pgtestdb.NoopMigrator{}, stdpgxfx.NewPgxV5Driver(), "rw", "ro"),
		fx.Populate(&deps),
	)
	app.RequireStart()
	tb.Cleanup(app.RequireStop)

	return deps
}

func currentDatabase(tb testing.TB, pool *pgxpool.Pool) string {
	tb.Helper()

	var name string
	require.NoError(tb, pool.QueryRow(tb.Context(), `SELECT current_database()`).Scan(&name))

	return name
}

func TestPoolsShareOneDatabase(t *testing.T) {
	t.Parallel()

	deps := setup(t)
	require.Equal(t, currentDatabase(t, deps.RW), currentDatabase(t, deps.RO))

	_, err := deps.RW.Exec(t.Context(), `CREATE TABLE item (id integer PRIMARY KEY)`)
	require.NoError(t, err)
	_, err = deps.RW.Exec(t.Context(), `INSERT INTO item VALUES (1)`)
	require.NoError(t, err)

	var count int
	require.NoError(t, deps.RO.QueryRow(t.Context(), `SELECT count(*) FROM item`).Scan(&count))
	require.Equal(t, 1, count, "the read-only pool sees the read-write pool's writes")

	_, err = deps.RO.Exec(t.Context(), `INSERT INTO item VALUES (2)`)
	require.ErrorContains(t, err, "read-only transaction", "the read-only pool keeps its deriver")
}

func TestAppsGetSeparateDatabases(t *testing.T) {
	t.Parallel()

	first, second := setup(t), setup(t)
	require.NotEqual(t, currentDatabase(t, first.RW), currentDatabase(t, second.RW))
}
