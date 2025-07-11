package stdpgxtxfx_test

import (
	"testing"

	"github.com/advdv/stdgo/fx/stdpgxfx"
	"github.com/advdv/stdgo/fx/stdpgxtxfx"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/advdv/stdgo/stdtx"
	"github.com/jackc/pgx/v5"
	"github.com/peterldowns/pgtestdb"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func TestSetup(t *testing.T) {
	t.Parallel()

	ro, rw := setup(t)
	require.NotNil(t, ro)
	require.NotNil(t, rw)
}

func setup(tb testing.TB) (*stdtx.Transactor[pgx.Tx], *stdtx.Transactor[pgx.Tx]) {
	var deps struct {
		fx.In
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
		stdpgxtxfx.TestProvide("testapp", "some_sysuser", "some_passwrd"),

		fx.Populate(&deps),
	)

	app.RequireStart()
	tb.Cleanup(app.RequireStop)

	return deps.RO, deps.RW
}
