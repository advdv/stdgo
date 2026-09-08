package stdpgxfx_test

import (
	"testing"

	"github.com/advdv/stdgo/fx/stdpgxfx"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"
)

// Return the configuration itself so the test can inspect what the driver received.
type configDriver struct{}

func (configDriver) NewPool(cfg *pgxpool.Config) (*pgxpool.Config, error) { return cfg, nil }
func (configDriver) Close(*pgxpool.Config) error                          { return nil }

func TestFinalPoolConfig(t *testing.T) {
	_, shared := setup(t)
	var deps struct {
		fx.In
		Base   *pgxpool.Config
		RW     stdpgxfx.FinalPgxPoolConfig `name:"rw"`
		RO     stdpgxfx.FinalPgxPoolConfig `name:"ro"`
		RWPool *pgxpool.Config             `name:"rw"`
		ROPool *pgxpool.Config             `name:"ro"`
	}
	calls := 0
	app := fxtest.New(t, shared,
		stdpgxfx.Provide(configDriver{}, "rw", "ro"),
		fx.Decorate(func(cfg stdpgxfx.Config) stdpgxfx.Config {
			cfg.Hosts = map[string]string{"rw": "writer.example.com"}
			return cfg
		}),
		stdpgxfx.ProvideDeriver("rw", func(_ *zap.Logger, cfg *pgxpool.Config) *pgxpool.Config {
			calls++
			cfg.ConnConfig.Host = "overridden.example.com"
			cfg.ConnConfig.RuntimeParams["application_name"] = "rw-app"
			return cfg
		}),
		stdpgxfx.ProvideDeriver("ro", func(_ *zap.Logger, cfg *pgxpool.Config) *pgxpool.Config {
			cfg.ConnConfig.Host = "db.cluster-abc.eu-west-1.rds.amazonaws.com"
			return cfg
		}),
		fx.Populate(&deps),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)
	require.Equal(t, 1, calls)
	require.Equal(t, "localhost", deps.Base.ConnConfig.Host)
	require.Equal(t, "writer.example.com", deps.RW.ConnConfig.Host)
	require.Equal(t, "db.cluster-ro-abc.eu-west-1.rds.amazonaws.com", deps.RO.ConnConfig.Host)
	require.Equal(t, deps.RW.ConnConfig.Host, deps.RWPool.ConnConfig.Host)
	require.Equal(t, deps.RO.ConnConfig.Host, deps.ROPool.ConnConfig.Host)
	deps.RWPool.ConnConfig.RuntimeParams["application_name"] = "driver-mutation"
	require.Equal(t, "rw-app", deps.RW.ConnConfig.RuntimeParams["application_name"])
	require.Empty(t, deps.RO.ConnConfig.RuntimeParams["application_name"])
}
