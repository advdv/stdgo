package stdriverfx_test

import (
	"testing"

	"github.com/advdv/stdgo/fx/stdpgxfx"
	"github.com/advdv/stdgo/fx/stdriverfx"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"
)

func TestListenerPoolConfig(t *testing.T) {
	for _, override := range []bool{false, true} {
		name := "defaults"
		if override {
			name = "override"
		}
		t.Run(name, func(t *testing.T) {
			source, err := pgxpool.ParseConfig("postgres://user:password@localhost/app?application_name=app&search_path=workers")
			require.NoError(t, err)
			source.MinConns, source.MinIdleConns, source.MaxConns = 3, 2, 10
			var deps struct {
				fx.In
				Pool *pgxpool.Pool `name:"river-listener"`
			}
			opts := []fx.Option{
				fx.Supply(zap.NewNop()),
				fx.Supply(fx.Annotate(stdpgxfx.FinalPgxPoolConfig{Config: source}, fx.ResultTags(`name:"rw"`))),
				stdriverfx.ProvideListenerPool("rw"), fx.Populate(&deps),
			}
			if override {
				opts = append(opts, stdpgxfx.ProvideDeriver("river-listener", func(_ *zap.Logger, cfg *pgxpool.Config) *pgxpool.Config {
					cfg.ConnConfig.Host = "direct.example.com"
					return cfg
				}))
			}
			app := fxtest.New(t, opts...)
			app.RequireStart()
			t.Cleanup(app.RequireStop)
			cfg := deps.Pool.Config()
			require.Zero(t, cfg.MinConns)
			require.Zero(t, cfg.MinIdleConns)
			require.EqualValues(t, 1, cfg.MaxConns)
			require.Equal(t, "app", cfg.ConnConfig.Database)
			require.Equal(t, "user", cfg.ConnConfig.User)
			require.Equal(t, "workers", cfg.ConnConfig.RuntimeParams["search_path"])
			require.Equal(t, "river-listener", cfg.ConnConfig.RuntimeParams["application_name"])
			if override {
				require.Equal(t, "direct.example.com", cfg.ConnConfig.Host)
			} else {
				require.Equal(t, "localhost", cfg.ConnConfig.Host)
			}
			require.Equal(t, "localhost", source.ConnConfig.Host)
			require.Equal(t, "app", source.ConnConfig.RuntimeParams["application_name"])
			require.EqualValues(t, 3, source.MinConns)
			app.RequireStop()
			_, err = deps.Pool.Acquire(t.Context())
			require.ErrorContains(t, err, "closed pool")
		})
	}
}
