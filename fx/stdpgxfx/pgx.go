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

// New build the main components of this package.
func New(params Params) (res Result, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), params.Config.PoolConnectionTimeout)
	defer cancel()

	res.RW, err = pgxpool.NewWithConfig(ctx, params.PgxPoolConfig)
	if err != nil {
		return res, fmt.Errorf("failed to connect pool: %w", err)
	}

	params.Lifecycle.Append(fx.Hook{OnStop: closePoolHook(params.Logs, params.Config, res.RW)})

	return res, nil
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
func Provide() fx.Option {
	return stdfx.ZapEnvCfgModule[Config]("stdpgx",
		New, fx.Provide(fx.Private, NewPoolConfig),
	)
}
