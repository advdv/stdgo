// Package stdpgxfx provides postgres pgx package as fx dependencies.
package stdpgxfx

import (
	"context"
	"fmt"
	"time"

	"github.com/advdv/stdgo/stdfx"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// Config configures the components.
type Config struct {
	// RWDatabaseURL configures the database connection string for the read-write connection.
	RWDatabaseURL string `env:"RW_DATABASE_URL"`
	// PoolConnectionTimeout configures how long the pgx pool connect logic waits for the connection to establish
	PoolConnectionTimeout time.Duration `env:"POOL_CONNECTION_TIMEOUT" envDefault:"5s"`
	// PoolCloseTimeout is the time we'll allow to to close the connection pool. Only effective if shorter than
	// the fx shutdown timeout
	PoolCloseTimeout time.Duration `env:"POOL_CLOSE_TIMEOUT" envDefault:"5s"`
}

// NewPoolConfig inits a pool configuration from the package config.
func NewPoolConfig(cfg Config, logs *zap.Logger) (*pgxpool.Config, error) {
	pcfg, err := pgxpool.ParseConfig(cfg.RWDatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse connecting string: %w", err)
	}

	logs.Info("initialized connection config",
		zap.Any("runtime_params", pcfg.ConnConfig.RuntimeParams),
		zap.Int32("pool_max_conns", pcfg.MaxConns),
		zap.String("user", pcfg.ConnConfig.User),
		zap.String("database", pcfg.ConnConfig.Database),
		zap.String("host", pcfg.ConnConfig.Host))

	return pcfg, nil
}

// Params define the dependencies of the main component(s).
type Params struct {
	fx.In
	fx.Lifecycle
	Logs          *zap.Logger
	Config        Config
	PgxPoolConfig *pgxpool.Config
}

// Result declare the main components produced by this package.
type Result struct {
	fx.Out
	RW *pgxpool.Pool `name:"rw"`
}

// New build the main components of this package.
func New(params Params) (res Result, err error) {
	res.RW, err = newPool(params.Lifecycle, params.Config, params.PgxPoolConfig, params.Logs)
	return res, err
}

// newPool is used for initializing a connection pool based on configs so we can also used it to
// created derived connection pools.
func newPool(lc fx.Lifecycle, cfg Config, ccfg *pgxpool.Config, logs *zap.Logger) (*pgxpool.Pool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cfg.PoolConnectionTimeout)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, ccfg)
	if err != nil {
		return nil, fmt.Errorf("failed to connect pool: %w", err)
	}

	lc.Append(fx.Hook{OnStop: closePoolHook(logs, cfg, pool)})

	return pool, nil
}

// closePoolHook will attempt to close the connection pool but continues shutdown if the ctx expires. We cannot pass a
// context to the pool's close so we will have to continue shutdown, this seems to be ok:
// https://github.com/jackc/pgx/issues/802
func closePoolHook(logs *zap.Logger, cfg Config, rw interface{ Close() }) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, cfg.PoolCloseTimeout)
		defer cancel()

		done := make(chan struct{})
		go func() { rw.Close(); close(done) }()

		select {
		case <-ctx.Done():
			err := ctx.Err()
			logs.Warn("failed to close connection pool in time",
				zap.Duration("timeout", cfg.PoolCloseTimeout), zap.Error(err))
			return err
		case <-done:
			logs.Info("connection pool was closed")

			return nil
		}
	}
}

// Provide components as fx dependencies.
func Provide(derivedPoolNames ...string) fx.Option {
	return stdfx.ZapEnvCfgModule[Config]("stdpgx",
		New,
		fx.Provide(fx.Private, NewPoolConfig),
		withDerivedPools(derivedPoolNames...),
	)
}

// Deriver needs to be provided by the user of this module if derived pools are created.
type Deriver func(base *pgxpool.Config) *pgxpool.Config

// ProvideDeriver is a short-hande function for providing a named deriver function that.
func ProvideDeriver(name string, deriver Deriver) fx.Option {
	return fx.Supply(fx.Annotate(deriver, fx.ResultTags(`name:"`+name+`"`)))
}

// withDerivedPools dynamically adds fx provides for creating connection pools that
// take the base pool as input.
func withDerivedPools(names ...string) fx.Option {
	options := make([]fx.Option, 0, len(names))
	for _, name := range names {
		options = append(options, fx.Provide(
			fx.Annotate(func(
				base *pgxpool.Pool, deriver Deriver, lc fx.Lifecycle, cfg Config, logs *zap.Logger,
			) (derived *pgxpool.Pool, err error) {
				// here, we derive the acual pool. We use the same constructor as the main but with a new configuration
				// that is build from the deriver, given the base pool.
				derived, err = newPool(lc, cfg, deriver(base.Config().Copy()), logs)
				if err != nil {
					return nil, fmt.Errorf("failed to created derived pool: %w", err)
				}

				logs.Info("initialized derived pool", zap.String("name", name))

				return derived, nil
			}, fx.ParamTags(`name:"rw"`, `name:"`+name+`"`), fx.ResultTags(`name:"`+name+`"`))))
	}

	return fx.Options(options...)
}
