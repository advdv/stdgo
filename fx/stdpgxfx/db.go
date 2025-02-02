// Package stdpgxfx provides sql.DB connection pools usng pgx/v5.
package stdpgxfx

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/advdv/stdgo/stdfx"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// Config configures the module.
type Config struct {
	// RWDatabaseURL configures the database connection string for the read-write connection.
	RWDatabaseURL string `env:"RW_DATABASE_URL"`
}

type (
	// Params describe the main parameters for providing components.
	Params struct {
		fx.In
		Config Config
	}

	// Result describe the main components provided for this module.
	Result struct {
		fx.Out
		PoolConfig *pgxpool.Config
	}
)

// New is the main constructor. In this package it only provides the pool configuration
// used through out the package.
func New(p Params) (r Result, err error) {
	pcfg, err := pgxpool.ParseConfig(p.Config.RWDatabaseURL)
	if err != nil {
		return r, fmt.Errorf("failed to parse connecting string: %w", err)
	}

	return Result{PoolConfig: pcfg}, nil
}

// newDB is the low-level constructor for turning our config into sql databases. It is
// called for both the main pool and the derived pools.
func newDB(
	deriver Deriver, // optional
	lc fx.Lifecycle,
	_ Config,
	pcfg *pgxpool.Config,
	_ *zap.Logger,
) (*sql.DB, error) {
	if deriver != nil {
		pcfg = deriver(pcfg.Copy())
	}
	db := stdlib.OpenDB(*pcfg.ConnConfig)
	lc.Append(fx.Hook{
		OnStop: func(context.Context) error {
			return db.Close()
		},
	})

	return db, nil
}

// Provide components as fx dependencies.
func Provide(mainPoolName string, derivedPoolNames ...string) fx.Option {
	return stdfx.ZapEnvCfgModule[Config]("stdpgx", New,
		// provide the "main" pool
		fx.Provide(fx.Annotate(newDB,
			fx.ParamTags(`name:"`+mainPoolName+`" optional:"true"`),
			fx.ResultTags(`name:"`+mainPoolName+`"`))),
		// provide the "derived" pools (if any)
		withDerivedPools(mainPoolName, derivedPoolNames...),
	)
}

// Deriver needs to be provided by the user of this module if derived pools are created.
type Deriver func(base *pgxpool.Config) *pgxpool.Config

// ProvideDeriver is a short-hande function for providing a named deriver function that.
func ProvideDeriver(name string, deriver Deriver) fx.Option {
	return fx.Provide(
		fx.Annotate(func() Deriver {
			return deriver
		}, fx.ResultTags(`name:"`+name+`"`)))
}

// withDerivedPools dynamically adds fx provides for creating connection pools that
// take the base pool as input.
func withDerivedPools(mainName string, names ...string) fx.Option {
	options := make([]fx.Option, 0, len(names))
	for _, name := range names {
		options = append(options, fx.Provide(
			fx.Annotate(func(
				_ *sql.DB, deriver Deriver, pcfg *pgxpool.Config, lc fx.Lifecycle, cfg Config, logs *zap.Logger,
			) (derived *sql.DB, err error) {
				// create the pool for each named derived.
				derived, err = newDB(deriver, lc, cfg, pcfg, logs)
				if err != nil {
					return nil, fmt.Errorf("failed to created derived pool: %w", err)
				}

				logs.Info("initialized derived pool", zap.String("name", name))
				return derived, nil
			},
				fx.ParamTags(`name:"`+mainName+`"`, `name:"`+name+`" optional:"true"`),
				fx.ResultTags(`name:"`+name+`"`))))
	}

	return fx.Options(options...)
}
