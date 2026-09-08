package stdriverfx

import (
	"context"
	"fmt"

	"github.com/advdv/stdgo/fx/stdpgxfx"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// ProvideListenerPool provides *pgxpool.Pool named "river-listener" from the
// source pool's named stdpgxfx.FinalPgxPoolConfig. It defaults to zero minimum
// connections and one maximum connection, suitable for one River client.
// An optional stdpgxfx.Deriver named "river-listener" can override these defaults
// or route listening around transaction-pooling PgBouncer. Both pools must
// connect to the same database and resolve the same River schema.
//
// River hijacks listener connections: they no longer count toward MaxConns and
// pool.Close does not close them. The River client must depend on this pool and
// register a stop hook so it stops before the pool closes. Each running client
// needs its own listener connection, even when sharing this pool.
func ProvideListenerPool(sourcePoolName string) fx.Option {
	return fx.Provide(fx.Annotate(
		newListenerPool,
		fx.ParamTags("", fmt.Sprintf(`name:%q`, sourcePoolName), `name:"river-listener" optional:"true"`),
		fx.ResultTags(`name:"river-listener"`),
	))
}

func newListenerPool(
	lifecycle fx.Lifecycle,
	source stdpgxfx.FinalPgxPoolConfig,
	deriver stdpgxfx.Deriver,
	logs *zap.Logger,
) (*pgxpool.Pool, error) {
	cfg := source.Copy()
	cfg.MinConns = 0
	cfg.MinIdleConns = 0
	cfg.MaxConns = 1
	cfg.ConnConfig.RuntimeParams["application_name"] = "river-listener"
	if deriver != nil {
		cfg = deriver(logs, cfg)
	}
	pool, err := stdpgxfx.NewPgxV5Driver().NewPool(cfg)
	if err != nil {
		return nil, err
	}
	lifecycle.Append(fx.Hook{OnStop: func(context.Context) error {
		pool.Close()
		return nil
	}})
	return pool, nil
}
